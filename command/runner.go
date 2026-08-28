package command

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/alvnukov/cozy-tools/config"
	"github.com/alvnukov/cozy-tools/evidence"
	"github.com/alvnukov/cozy-tools/security"
	"github.com/alvnukov/cozy-tools/vars"
)

// Context keys for per-request secret injection.
type contextKey string

const (
	secretEnvsKey    contextKey = "secret_envs"
	secretMaskKey    contextKey = "secret_mask"
	processWaitDelay            = 2 * time.Second
	abortStatusWait             = processWaitDelay + time.Second
)

// ContextWithSecrets stores resolved secret env vars and mask in the context.
func ContextWithSecrets(ctx context.Context, envs []string, mask *security.Mask) context.Context {
	ctx = context.WithValue(ctx, secretEnvsKey, envs)
	ctx = context.WithValue(ctx, secretMaskKey, mask)
	return ctx
}

func secretsFromContext(ctx context.Context) ([]string, *security.Mask) {
	envs, _ := ctx.Value(secretEnvsKey).([]string)
	mask, _ := ctx.Value(secretMaskKey).(*security.Mask)
	return envs, mask
}

const execValuesKey contextKey = "exec_values"

// Exec carries the value channels of one execution to the runner: template
// variables applied to command and stdin at execution time, and stdin content
// piped to the process. Records keep the template as written, so substituted
// secret values never reach history.
type Exec struct {
	// Vars are substituted into the command template before the shell
	// parses it: the legacy raw channel, unsafe for values from outside
	// the host's trust boundary (see the package trust model in doc.go).
	Vars map[string]string
	// Argv names the program and its arguments for injection-safe
	// execution without a shell; set by Runner.RunArgv.
	Argv []string
	// Env carries per-execution environment entries to the process
	// verbatim; values never become shell source.
	Env map[string]string
	// Stdin is piped to the process as data.
	Stdin string
}

// ContextWithExec stores per-execution value channels in the context.
func ContextWithExec(ctx context.Context, exec Exec) context.Context {
	return context.WithValue(ctx, execValuesKey, exec)
}

func execFromContext(ctx context.Context) Exec {
	values, _ := ctx.Value(execValuesKey).(Exec)
	return values
}

// PrepareExec validates the value channels of one execution — secret handles,
// explicit vars and env, stdin or stdin_var — and returns the context the
// runner consumes. It fails closed on every ambiguity before anything runs.
func PrepareExec(ctx context.Context, resolver config.ValueResolver, handles []string, explicitVars map[string]string, explicitEnv map[string]string, stdin string, stdinVar string) (context.Context, error) {
	if stdin != "" && stdinVar != "" {
		return ctx, errors.New("stdin and stdin_var are mutually exclusive; pass stdin content or a var name, not both")
	}
	if resolver == nil {
		return ctx, errors.New("resolver is required")
	}
	resolved, err := resolver.ResolveValues(handles, explicitVars, explicitEnv)
	if err != nil {
		return ctx, err
	}
	if stdinVar != "" {
		value, ok := resolved.Vars[stdinVar]
		if !ok {
			return ctx, fmt.Errorf("stdin_var %q has no value; pass it in vars or secret_handles", stdinVar)
		}
		stdin = value
	}
	if resolved.Mask != nil || len(resolved.Env) > 0 {
		ctx = ContextWithSecrets(ctx, resolved.Env, resolved.Mask)
	}
	if len(resolved.Vars) > 0 || stdin != "" {
		ctx = ContextWithExec(ctx, Exec{Vars: resolved.Vars, Stdin: stdin})
	}
	return ctx, nil
}

type activeCommand struct {
	cancel context.CancelFunc
	done   <-chan struct{}
}

// Runner executes shell commands under repository and output policies.
type Runner struct {
	policy   config.CommandPolicy
	history  *History
	baseMask *security.Mask
	// running is the authoritative process tracker until terminal history
	// publication completes. Only the worker removes entries.
	running sync.Map // map[string]activeCommand
}

// Result is the compact, redacted command execution record returned to callers.
type Result struct {
	Status        string          `json:"status,omitempty"`
	CommandID     string          `json:"command_id"`
	Command       string          `json:"command"`
	CWD           string          `json:"cwd"`
	ExitCode      int             `json:"exit_code"`
	DurationMS    int64           `json:"duration_ms"`
	Truncated     bool            `json:"truncated"`
	StdoutTail    []string        `json:"stdout_tail"`
	StderrTail    []string        `json:"stderr_tail"`
	FilteredLines []string        `json:"filtered_lines,omitempty"`
	EvidenceLines []evidence.Line `json:"evidence_lines,omitempty"`
	OutputHash    string          `json:"output_hash"`
	NextCall      *NextCall       `json:"next_call,omitempty"`
	Previous      *PreviousRun    `json:"previous,omitempty"`
	// FailureMarkers holds lines that report a failure the exit code did not,
	// which happens whenever a command is piped into tail, grep or head.
	FailureMarkers []string `json:"failure_markers,omitempty"`
	// EvidenceDistilled records whether EvidenceLines selected the lines that
	// report a failure or only fell back to the tail of the output. It never
	// travels: it exists so the response layer can drop a fallback that repeats
	// StdoutTail, instead of re-deriving by text comparison what the selection
	// already knew. Comparing text cannot tell a failure line that happens to
	// sit in the tail from a copy of the tail.
	EvidenceDistilled bool `json:"-"`
}

// PreviousRun reports that the same command already ran in the same repository
// shortly before this one.
//
// The runner does not refuse a repeat: a command may be repeated for good
// reason, and a check that ran before an edit is not the same check after it.
// What it can do is remove the guesswork. SameOutput means the two runs produced
// byte-identical output, which is as close as this layer gets to saying the
// repeat learned nothing.
type PreviousRun struct {
	CommandID  string `json:"command_id"`
	Status     string `json:"status"`
	ExitCode   int    `json:"exit_code"`
	AgeSeconds int64  `json:"age_seconds"`
	SameOutput bool   `json:"same_output"`
}

// previousRunWindow bounds how far back a repeat is worth reporting. Older runs
// are history, not duplication.
const previousRunWindow = time.Hour

// NextCall tells the caller how to inspect a still-running durable command.
type NextCall struct {
	Tool      string `json:"tool"`
	Action    string `json:"action,omitempty"`
	CommandID string `json:"command_id"`
	Mode      string `json:"mode,omitempty"`
}

// Filter selects a compact slice from command output before it reaches the caller.
type Filter struct {
	Include         string   `json:"include"`
	Exclude         string   `json:"exclude"`
	CaseInsensitive bool     `json:"case_insensitive"`
	MaxLines        int      `json:"max_lines"`
	ContextBefore   int      `json:"context_before"`
	ContextAfter    int      `json:"context_after"`
	Preset          string   `json:"preset"`
	Packs           []string `json:"packs"`
	Regexes         []string `json:"regexes"`
	Keywords        []string `json:"keywords"`
}

// NewRunner creates a command runner from policy limits.
func NewRunner(policy config.CommandPolicy) *Runner {
	return NewRunnerWithMask(policy, nil)
}

// NewRunnerWithMask creates a command runner that redacts configured secrets from all retained output.
func NewRunnerWithMask(policy config.CommandPolicy, mask *security.Mask) *Runner {
	if policy.LogEnabled != nil && !*policy.LogEnabled {
		return &Runner{policy: policy, history: NewInMemoryHistory(), baseMask: mask}
	}
	history, err := NewHistory(HistoryPolicy{Dir: policy.LogDir, RetentionDays: policy.LogRetentionDays, MaxRecords: policy.LogMaxRecords, Compress: policy.LogCompress})
	if err != nil {
		fmt.Fprintf(os.Stderr, "command history from %q: %v; falling back to in-memory\n", policy.LogDir, err)
		history = NewInMemoryHistory()
	}
	return &Runner{policy: policy, history: history, baseMask: mask}
}

// Run executes cmd in cwd after validating cwd against the configured allowlist.
func (r *Runner) Run(ctx context.Context, cmd string, cwd string, timeoutSeconds int) (Result, error) {
	return r.RunFiltered(ctx, cmd, cwd, timeoutSeconds, Filter{})
}

// RunFiltered executes cmd and applies a deterministic grep-like output filter.
func (r *Runner) RunFiltered(ctx context.Context, cmd string, cwd string, timeoutSeconds int, filter Filter) (Result, error) {
	return r.RunFilteredWithWait(ctx, cmd, cwd, timeoutSeconds, 0, filter)
}

// RunFilteredWithWait runs a command with a separate MCP wait budget.
// If mcpWaitSeconds is exceeded, the process keeps running under its execution timeout
// and the caller receives a durable command_id for command action=get/filter.
func (r *Runner) RunFilteredWithWait(ctx context.Context, cmd string, cwd string, timeoutSeconds int, mcpWaitSeconds int, filter Filter) (Result, error) {
	return r.runFilteredWithWait(ctx, cmd, cwd, timeoutSeconds, mcpWaitSeconds, filter, "")
}

func (r *Runner) runFilteredWithWait(
	ctx context.Context,
	cmd string,
	cwd string,
	timeoutSeconds int,
	mcpWaitSeconds int,
	filter Filter,
	repoPath string,
) (Result, error) {
	if strings.TrimSpace(cmd) == "" {
		return Result{}, errors.New("command is required")
	}
	if err := rejectProtectedCommand(cmd, r.policy); err != nil {
		return Result{}, err
	}
	runCWD, err := r.safeCWD(cwd)
	if err != nil {
		return Result{}, err
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = r.policy.DefaultTimeoutSeconds
	}
	return r.runPreparedWithWait(ctx, cmd, runCWD, timeoutSeconds, mcpWaitSeconds, filter, repoPath)
}

func (r *Runner) runPreparedWithWait(ctx context.Context, cmd string, runCWD string, timeoutSeconds int, mcpWaitSeconds int, filter Filter, repoPath string) (Result, error) {
	if _, _, err := applyFilter(nil, filter); err != nil {
		return Result{}, err
	}
	commandID := newCommandID()
	started := time.Now().UTC()
	if mcpWaitSeconds > 0 {
		if err := r.history.Put(Record{
			CommandID: commandID,
			Status:    "running",
			RepoPath:  repoPath,
			Command:   r.maskText(ctx, cmd),
			CWD:       runCWD,
			ExitCode:  -1,
			StartedAt: started,
			CreatedAt: started,
		}); err != nil {
			return Result{}, err
		}
	}

	execute := func(execCtx context.Context) (Result, error) {
		return r.executePrepared(execCtx, commandID, cmd, runCWD, timeoutSeconds, filter, repoPath, started)
	}
	if mcpWaitSeconds <= 0 {
		return execute(ctx)
	}

	cmdCtx, cmdCancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	r.running.Store(commandID, activeCommand{cancel: cmdCancel, done: done})

	var result Result
	var err error
	go func() {
		defer func() {
			r.running.Delete(commandID)
			close(done)
		}()
		result, err = execute(cmdCtx)
	}()

	timer := time.NewTimer(time.Duration(mcpWaitSeconds) * time.Second)
	defer timer.Stop()
	select {
	case <-done:
		return result, err
	case <-timer.C:
		return Result{
			Status:     "running",
			CommandID:  commandID,
			Command:    r.maskText(ctx, cmd),
			CWD:        runCWD,
			ExitCode:   -1,
			DurationMS: time.Since(started).Milliseconds(),
			NextCall:   nextCallForStatus("running", commandID),
		}, nil
	}
}

func (r *Runner) executePrepared(ctx context.Context, commandID string, cmd string, runCWD string, timeoutSeconds int, filter Filter, repoPath string, started time.Time) (Result, error) {
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	envs, cmdMask := secretsFromContext(ctx)
	redactOutput := func(text string) string {
		out := redact(text)
		if r.baseMask != nil {
			out = r.baseMask.Apply(out)
		}
		if cmdMask != nil {
			out = cmdMask.Apply(out)
		}
		return out
	}
	var output *tailOutput
	updateRunningOutput := func(stdoutText string, stderrText string, outputTruncated bool) {
		stdoutLines := normalizeLines(stdoutText)
		stderrLines := normalizeLines(stderrText)
		combined := normalizeLines(output.combinedText())
		sum := output.currentSum()
		_ = r.history.UpdateRunningOutput(commandID, stdoutLines, stderrLines, combined, outputTruncated, hex.EncodeToString(sum[:]))
	}
	resolvedCmd := cmd
	execVals := execFromContext(ctx)
	resolvedStdin := execVals.Stdin
	if len(execVals.Argv) > 0 {
		if len(execVals.Vars) > 0 {
			safeErr := errors.New("argv execution does not support template variables; pass values as argv elements, environment entries, or stdin")
			r.recordFailedPreparation(ctx, commandID, cmd, runCWD, repoPath, started, safeErr)
			return Result{}, safeErr
		}
	} else if len(execVals.Vars) > 0 {
		substituted, subErr := vars.Substitute(cmd, execVals.Vars)
		if subErr != nil {
			safeErr := errors.New(redactOutput(subErr.Error()))
			r.recordFailedPreparation(ctx, commandID, cmd, runCWD, repoPath, started, safeErr)
			return Result{}, safeErr
		}
		resolvedCmd = substituted
		if resolvedStdin != "" {
			substituted, subErr := vars.Substitute(resolvedStdin, execVals.Vars)
			if subErr != nil {
				safeErr := errors.New(redactOutput(subErr.Error()))
				r.recordFailedPreparation(ctx, commandID, cmd, runCWD, repoPath, started, safeErr)
				return Result{}, safeErr
			}
			resolvedStdin = substituted
		}
	}
	output = newTailOutput(r.policy.MaxOutputBytes, redactOutput, updateRunningOutput)
	var command *exec.Cmd
	if len(execVals.Argv) > 0 {
		// #nosec G204 -- argv execution is this package's explicit capability; elements are data by construction and never reach a shell.
		command = exec.CommandContext(runCtx, execVals.Argv[0], execVals.Argv[1:]...)
	} else {
		// #nosec G204 -- command execution is this package's explicit MCP capability and is constrained by cwd, timeout, and output policy.
		command = exec.CommandContext(runCtx, shellBin(), shellArgs(resolvedCmd)...)
	}
	command.Dir = runCWD
	termination := prepareCommandTermination(command)
	if len(envs) > 0 || len(execVals.Env) > 0 {
		command.Env = mergeEnv(os.Environ(), envs, envSlice(execVals.Env))
	}
	if resolvedStdin != "" {
		command.Stdin = strings.NewReader(resolvedStdin)
	}
	command.Stdout = output.stdoutWriter()
	command.Stderr = output.stderrWriter()

	// run kills the process group before the leader is reaped, so the pgid
	// cannot have been recycled by the time the kill fires.
	err := termination.run(command)
	completed := time.Now().UTC()
	duration := completed.Sub(started)

	exitCode := 0
	status := "ok"
	executionError := ""
	if err != nil {
		var exitErr *exec.ExitError
		switch {
		case errors.Is(runCtx.Err(), context.DeadlineExceeded):
			exitCode = 124
			status = "timeout"
		case errors.Is(runCtx.Err(), context.Canceled):
			exitCode = 130
			status = "aborted"
		case errors.As(err, &exitErr):
			exitCode = exitErr.ExitCode()
			status = "failed"
		default:
			exitCode = 1
			status = "failed"
			executionError = redactOutput(err.Error())
		}
	}
	if exitCode != 0 && status == "ok" {
		status = "failed"
	}

	stdoutText, stderrText, outputTruncated := output.snapshot()
	stdoutText = redactOutput(stdoutText)
	stderrText = redactOutput(stderrText)
	stdoutLines := normalizeLines(stdoutText)
	stderrLines := normalizeLines(stderrText)
	if executionError != "" {
		stderrLines = append(stderrLines, executionError)
	}
	// Combined keeps the order the bytes actually arrived across both
	// streams, so evidence reads in the order the command produced it.
	combined := normalizeLines(output.combinedText())
	if executionError != "" {
		combined = append(combined, executionError)
	}
	evidenceLines := combined
	truncatedLines := false
	if len(evidenceLines) > r.policy.MaxLines {
		truncatedLines = true
		evidenceLines = tailN(evidenceLines, r.policy.MaxLines)
	}
	// The hash covers the complete output of both streams, not just the
	// retained tail, so identical runs hash identically whatever the budget.
	sum := output.finalSum()
	filteredLines, filterTruncated, err := applyFilter(combined, filter)
	if err != nil {
		return Result{}, err
	}
	outputHash := hex.EncodeToString(sum[:])
	commandStr := r.maskText(ctx, cmd)
	truncated := outputTruncated || truncatedLines
	if err := r.history.Put(Record{
		CommandID:   commandID,
		Status:      status,
		RepoPath:    repoPath,
		Command:     commandStr,
		CWD:         runCWD,
		ExitCode:    exitCode,
		DurationMS:  duration.Milliseconds(),
		Truncated:   truncated,
		Stdout:      stdoutLines,
		Stderr:      stderrLines,
		Combined:    combined,
		OutputHash:  outputHash,
		StartedAt:   started,
		CompletedAt: completed,
	}); err != nil {
		return Result{}, err
	}
	selectedEvidence, evidenceDistilled := evidence.SelectDistilled(evidenceLines, 30)
	return Result{
		Status:        status,
		CommandID:     commandID,
		Command:       commandStr,
		CWD:           runCWD,
		ExitCode:      exitCode,
		DurationMS:    duration.Milliseconds(),
		Truncated:     truncated || filterTruncated,
		StdoutTail:    tail80(stdoutLines),
		StderrTail:    tail80(stderrLines),
		FilteredLines: filteredLines,
		EvidenceLines: selectedEvidence,
		OutputHash:    outputHash,
		Previous:      r.previousRun(repoPath, commandStr, commandID, outputHash, completed),

		FailureMarkers:    maskedFailureMarkers(exitCode, combined),
		EvidenceDistilled: evidenceDistilled,
	}, nil
}

// recordFailedPreparation closes a running history record for a command that
// never started: template substitution failed, and an orphaned running
// record would outlive the call that created it.
func (r *Runner) recordFailedPreparation(ctx context.Context, commandID string, cmd string, runCWD string, repoPath string, started time.Time, cause error) {
	if r.history == nil {
		return
	}
	now := time.Now().UTC()
	lines := []string{cause.Error()}
	_ = r.history.Put(Record{
		CommandID:   commandID,
		Status:      "failed",
		RepoPath:    repoPath,
		Command:     r.maskText(ctx, cmd),
		CWD:         runCWD,
		ExitCode:    1,
		DurationMS:  now.Sub(started).Milliseconds(),
		Stderr:      lines,
		Combined:    lines,
		StartedAt:   started,
		CompletedAt: now,
		CreatedAt:   now,
	})
}

// mergeEnv merges KEY=value pairs over base; a key set by a later overlay
// replaces its earlier value in place, so exactly one entry per key reaches
// the process and the last writer wins deterministically.
func mergeEnv(base []string, overlays ...[]string) []string {
	order := make([]string, 0, len(base))
	values := make(map[string]string, len(base))
	put := func(pair string) {
		key, value := pair, ""
		if k, v, ok := strings.Cut(pair, "="); ok {
			key, value = k, v
		}
		if key == "" {
			return
		}
		if _, seen := values[key]; !seen {
			order = append(order, key)
		}
		values[key] = value
	}
	for _, pair := range base {
		put(pair)
	}
	for _, overlay := range overlays {
		for _, pair := range overlay {
			put(pair)
		}
	}
	merged := make([]string, 0, len(order))
	for _, key := range order {
		merged = append(merged, key+"="+values[key])
	}
	return merged
}

// previousRun looks for a recent run of the same command in the same repository.
func (r *Runner) previousRun(repoPath string, command string, exceptID string, outputHash string, now time.Time) *PreviousRun {
	if r.history == nil || strings.TrimSpace(repoPath) == "" {
		return nil
	}
	entry, ok := r.history.lastIdenticalRun(repoPath, command, exceptID)
	if !ok {
		return nil
	}
	age := now.Sub(entry.CreatedAt)
	if age < 0 {
		age = 0
	}
	if age > previousRunWindow {
		return nil
	}
	return &PreviousRun{
		CommandID:  entry.CommandID,
		Status:     entry.Status,
		ExitCode:   entry.ExitCode,
		AgeSeconds: int64(age.Seconds()),
		SameOutput: outputHash != "" && entry.OutputHash == outputHash,
	}
}

func (r *Runner) maskText(ctx context.Context, text string) string {
	out := text
	_, cmdMask := secretsFromContext(ctx)
	if r.baseMask != nil {
		out = r.baseMask.Apply(out)
	}
	if cmdMask != nil {
		out = cmdMask.Apply(out)
	}
	return out
}

func newCommandID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return hex.EncodeToString(buf[:])
	}
	sum := sha256.Sum256([]byte(strconv.FormatInt(time.Now().UnixNano(), 10)))
	return hex.EncodeToString(sum[:8])
}

func nextCallForStatus(status string, commandID string) *NextCall {
	if status != "running" || commandID == "" {
		return nil
	}
	return &NextCall{Tool: "command", Action: "get", CommandID: commandID, Mode: "status"}
}

// RunInRepo executes cmd in repoPath or a repo-relative cwd without allowing path escape.
func (r *Runner) RunInRepo(ctx context.Context, cmd string, repoPath string, cwd string, timeoutSeconds int) (Result, error) {
	return r.RunFilteredInRepo(ctx, cmd, repoPath, cwd, timeoutSeconds, Filter{})
}

// RunInRepoWithWait executes cmd in a repo with a separate MCP wait budget.
func (r *Runner) RunInRepoWithWait(ctx context.Context, cmd string, repoPath string, cwd string, timeoutSeconds int, mcpWaitSeconds int) (Result, error) {
	return r.RunFilteredInRepoWithWait(ctx, cmd, repoPath, cwd, timeoutSeconds, mcpWaitSeconds, Filter{})
}

// RunFilteredInRepo executes cmd in a repo and applies a deterministic output filter.
func (r *Runner) RunFilteredInRepo(ctx context.Context, cmd string, repoPath string, cwd string, timeoutSeconds int, filter Filter) (Result, error) {
	return r.RunFilteredInRepoWithWait(ctx, cmd, repoPath, cwd, timeoutSeconds, 0, filter)
}

// RunFilteredInRepoWithWait executes cmd in a repo with bounded MCP wait and durable lookup.
func (r *Runner) RunFilteredInRepoWithWait(ctx context.Context, cmd string, repoPath string, cwd string, timeoutSeconds int, mcpWaitSeconds int, filter Filter) (Result, error) {
	if strings.TrimSpace(repoPath) == "" {
		return Result{}, errors.New("repo_path is required")
	}
	if strings.TrimSpace(cmd) == "" {
		return Result{}, errors.New("command is required")
	}
	if err := rejectProtectedCommand(cmd, r.policy); err != nil {
		return Result{}, err
	}
	if err := rejectShellSourceWrite(cmd, repoPath); err != nil {
		return Result{}, err
	}
	repo, err := resolveDir(repoPath)
	if err != nil {
		return Result{}, err
	}
	runCWD := repo
	if strings.TrimSpace(cwd) != "" {
		if filepath.IsAbs(cwd) {
			return Result{}, errors.New("cwd must be repo-relative when repo_path is set")
		}
		runCWD = filepath.Join(repo, filepath.Clean(cwd))
		if !realInsideDir(repo, runCWD) {
			return Result{}, fmt.Errorf("cwd %q escapes repo_path", cwd)
		}
	}
	runCWD, err = r.safeCWD(runCWD)
	if err != nil {
		return Result{}, err
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = r.policy.DefaultTimeoutSeconds
	}
	return r.runPreparedWithWait(ctx, cmd, runCWD, timeoutSeconds, mcpWaitSeconds, filter, repo)
}

// FilterHistory applies filter to a retained command output record.
func (r *Runner) FilterHistory(commandID string, filter Filter) (Result, error) {
	return r.history.Filter(commandID, filter)
}

const (
	// waitPollInterval bounds how often a wait re-reads the durable record of a
	// command some other helper process is running.
	waitPollInterval = 500 * time.Millisecond
	// maxWaitSeconds bounds how long a single get call may block.
	maxWaitSeconds = 600
)

// WaitForHistory returns a retained record, blocking until the command reaches a
// terminal state or waitSeconds elapses.
//
// Without it the only way to wait was to sleep inside a shell command, which
// spends a whole turn per poll to learn one bit the helper already knew, and
// leaves an orphaned waiter behind whenever the estimate is wrong.
//
// A wait that runs out is not an error: the caller gets the running record back
// and can decide whether to keep waiting.
func (r *Runner) WaitForHistory(ctx context.Context, commandID string, filter Filter, waitSeconds int) (Result, error) {
	result, err := r.FilterHistory(commandID, filter)
	if err != nil || waitSeconds <= 0 || isTerminalStatus(result.Status) {
		return result, err
	}
	if waitSeconds > maxWaitSeconds {
		waitSeconds = maxWaitSeconds
	}

	deadline := time.NewTimer(time.Duration(waitSeconds) * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(waitPollInterval)
	defer ticker.Stop()

	// A command this process started can be awaited directly. One started by
	// another helper sharing the log directory can only be polled.
	var finished <-chan struct{}
	if val, ok := r.running.Load(commandID); ok {
		if active, ok := val.(activeCommand); ok {
			finished = active.done
		}
	}

	for {
		select {
		case <-ctx.Done():
			return r.FilterHistory(commandID, filter)
		case <-deadline.C:
			return r.FilterHistory(commandID, filter)
		case <-finished:
			return r.FilterHistory(commandID, filter)
		case <-ticker.C:
			result, err := r.FilterHistory(commandID, filter)
			if err != nil {
				return Result{}, err
			}
			if isTerminalStatus(result.Status) {
				return result, nil
			}
		}
	}
}

// ListCommands returns a bounded list of command records from history.
func (r *Runner) ListCommands(req ListRequest) (ListResult, error) {
	return r.history.List(req)
}

// tracksCommand reports whether this runner owns commandID as an active
// process. Only the owning runner can abort it and await its done channel.
func (r *Runner) tracksCommand(commandID string) bool {
	_, ok := r.running.Load(commandID)
	return ok
}

// knowsCommand reports whether this runner holds a durable record for commandID.
func (r *Runner) knowsCommand(commandID string) bool {
	if r.history == nil {
		return false
	}
	_, found, err := r.history.getRecord(commandID)
	return err == nil && found
}

// AbortResult reports the outcome of an abort attempt.
type AbortResult struct {
	Status    string `json:"status"`
	CommandID string `json:"command_id"`
	Reason    string `json:"reason,omitempty"`
}

// Abort requests cancellation and waits briefly for terminal history publication.
// Status ok guarantees that subsequent get/filter calls no longer report running.
// Status running means cancellation was requested but publication is still pending.
func (r *Runner) Abort(commandID string) (AbortResult, error) {
	if strings.TrimSpace(commandID) == "" {
		return AbortResult{}, errors.New("command_id is required")
	}
	val, ok := r.running.Load(commandID)
	if !ok {
		record, found, err := r.history.getRecord(commandID)
		if err != nil {
			return AbortResult{}, err
		}
		if !found {
			return AbortResult{Status: "not_found", CommandID: commandID, Reason: "no such command"}, nil
		}
		if record.Status == "running" {
			return AbortResult{Status: "running", CommandID: commandID, Reason: "terminal state publication is pending"}, nil
		}
		return AbortResult{Status: "already_completed", CommandID: commandID, Reason: "command already finished"}, nil
	}
	active, ok := val.(activeCommand)
	if !ok {
		return AbortResult{}, errors.New("invalid active command in process tracker")
	}
	active.cancel()

	timer := time.NewTimer(abortStatusWait)
	defer timer.Stop()
	select {
	case <-active.done:
		record, found, err := r.history.getRecord(commandID)
		if err != nil {
			return AbortResult{}, err
		}
		if found && record.Status == "running" {
			return AbortResult{Status: "running", CommandID: commandID, Reason: "terminal state publication is pending"}, nil
		}
		return AbortResult{Status: "ok", CommandID: commandID}, nil
	case <-timer.C:
		return AbortResult{Status: "running", CommandID: commandID, Reason: "cancellation requested; terminal state publication is pending"}, nil
	}
}

// The denial has to name a way forward that exists. Full replacement was
// removed deliberately, so pointing at config_replace left the caller with a
// guard it could not satisfy and a tool it could not call.
// neutralDenyMessage is the host-neutral remediation text used when the
// policy supplies none: it names no host actions, because what a denied
// command should do instead is the embedding host's knowledge.
const neutralDenyMessage = "command denied: it references a protected path or marker of the embedding host; perform the change through the host's policy-approved interfaces"

// rejectProtectedCommand denies commands that reference the policy's
// protected config path or markers. Everything protected is policy state:
// the shared package contributes no paths or markers of its own.
func rejectProtectedCommand(cmd string, policy config.CommandPolicy) error {
	markers := make([]string, 0, 1+len(policy.ProtectedMarkers))
	if strings.TrimSpace(policy.ProtectedConfigPath) != "" {
		markers = append(markers, normalizeCommandPath(policy.ProtectedConfigPath))
	}
	for _, marker := range policy.ProtectedMarkers {
		if strings.TrimSpace(marker) == "" {
			continue
		}
		markers = append(markers, normalizeCommandPath(marker))
	}
	if len(markers) == 0 {
		return nil
	}
	normalized := normalizeCommandPath(cmd)
	message := policy.DenyMessage
	if message == "" {
		message = neutralDenyMessage
	}
	for _, marker := range markers {
		if strings.Contains(normalized, marker) {
			return fmt.Errorf("%s: command references %q", message, marker)
		}
	}
	return nil
}

func normalizeCommandPath(value string) string {
	return strings.ToLower(strings.ReplaceAll(value, "\\", "/"))
}

// CleanupHistory removes command log records that exceed retention policy limits.
// Safe to call multiple times; subsequent calls are no-ops when policy limits are satisfied.
func (r *Runner) CleanupHistory() error {
	return r.history.Cleanup()
}

func resolveDir(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("cwd %q is not a directory", abs)
	}
	return filepath.Clean(abs), nil
}

func resolveRealDir(path string) (string, error) {
	abs, err := resolveDir(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func (r *Runner) safeCWD(cwd string) (string, error) {
	abs, err := resolveDir(cwd)
	if err != nil {
		return "", err
	}
	for _, allowed := range r.policy.AllowedCWDs {
		allowedPath := allowed
		if !filepath.IsAbs(allowedPath) {
			allowedPath = filepath.Join(abs, allowedPath)
		}
		if realInsideDir(allowedPath, abs) {
			return abs, nil
		}
	}
	return "", fmt.Errorf("cwd %q is outside allowed_cwds", abs)
}

func insideDir(root string, child string) bool {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	childAbs, err := filepath.Abs(child)
	if err != nil {
		return false
	}
	return relativePathInside(rootAbs, childAbs)
}

func realInsideDir(root string, child string) bool {
	rootAbs, err := resolveRealDir(root)
	if err != nil {
		return false
	}
	childAbs, err := resolveRealDir(child)
	if err != nil {
		return false
	}
	return relativePathInside(rootAbs, childAbs)
}

func relativePathInside(root string, child string) bool {
	rel, err := filepath.Rel(root, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}

type liveOutput struct {
	mu     sync.Mutex
	stdout *limitBuffer
	stderr *limitBuffer
	update func(stdoutText string, stderrText string, truncated bool)
}

type liveOutputWriter struct {
	output *liveOutput
	stream string
}

func newLiveOutput(maxBytes int, update func(stdoutText string, stderrText string, truncated bool)) *liveOutput {
	return &liveOutput{stdout: newLimitBuffer(maxBytes), stderr: newLimitBuffer(maxBytes), update: update}
}

func (o *liveOutput) stdoutWriter() *liveOutputWriter {
	return &liveOutputWriter{output: o, stream: "stdout"}
}

func (o *liveOutput) stderrWriter() *liveOutputWriter {
	return &liveOutputWriter{output: o, stream: "stderr"}
}

func (w *liveOutputWriter) Write(p []byte) (int, error) {
	return w.output.write(w.stream, p)
}

func (o *liveOutput) write(stream string, p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	var n int
	var err error
	if stream == "stderr" {
		n, err = o.stderr.Write(p)
	} else {
		n, err = o.stdout.Write(p)
	}
	if o.update != nil {
		o.update(o.stdout.String(), o.stderr.String(), o.stdout.Truncated() || o.stderr.Truncated())
	}
	return n, err
}

func (o *liveOutput) snapshot() (string, string, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.stdout.String(), o.stderr.String(), o.stdout.Truncated() || o.stderr.Truncated()
}

type limitBuffer struct {
	buf       bytes.Buffer
	maxBytes  int
	truncated bool
}

func newLimitBuffer(maxBytes int) *limitBuffer {
	if maxBytes <= 0 {
		maxBytes = 200000
	}
	return &limitBuffer{maxBytes: maxBytes}
}

func (b *limitBuffer) Write(p []byte) (int, error) {
	if b.buf.Len()+len(p) <= b.maxBytes {
		_, _ = b.buf.Write(p)
		return len(p), nil
	}
	b.truncated = true
	remaining := b.maxBytes - b.buf.Len()
	if remaining > 0 {
		_, _ = b.buf.Write(p[:runeSafeCut(p, remaining)])
	}
	return len(p), nil
}

func (b *limitBuffer) String() string {
	return b.buf.String()
}

func (b *limitBuffer) Truncated() bool {
	return b.truncated
}

// runeSafeCut returns the largest cut <= limit that does not split a
// UTF-8 rune at the end of b.
func runeSafeCut(b []byte, limit int) int {
	cut := limit
	for cut > 0 && cut < len(b) && !utf8.RuneStart(b[cut]) {
		cut--
	}
	return cut
}

// TruncateUTF8 cuts s to at most limit bytes without splitting a UTF-8
// rune, so a truncated tail survives JSON encoding as text instead of
// U+FFFD.
func TruncateUTF8(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(authorization:\s*bearer\s+)[^\s]+`),
	regexp.MustCompile(`(?i)(x-api-key:\s*)[^\s]+`),
	regexp.MustCompile(`(?i)(private-token:\s*)[^\s]+`),
	regexp.MustCompile(`(?i)(api[_-]?key["']?\s*[:=]\s*["']?)[A-Za-z0-9._\-]+`),
	regexp.MustCompile(`(?i)(token["']?\s*[:=]\s*["']?)[A-Za-z0-9._\-]+`),
	regexp.MustCompile(`(?i)(secret["']?\s*[:=]\s*["']?)[A-Za-z0-9._\-]+`),
	regexp.MustCompile(`(?i)(password["']?\s*[:=]\s*["']?)[^\s"']+`),
	regexp.MustCompile(`(?:AKIA|ASIA)[A-Z0-9]{14,}`),
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9_]{36,}`),
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9._-]{10,}`),
}

func redact(text string) string {
	out := text
	for _, pattern := range secretPatterns {
		out = pattern.ReplaceAllString(out, `${1}[REDACTED]`)
	}
	return out
}

var builtInRegexPacks = map[string][]string{
	"errors-only": {
		`(?i)\b(error|failed|failure|panic|fatal|exception|timeout)\b`,
	},
	"test-failures": {
		`(?i)(^--- FAIL:|^FAIL\b|panic:|assert|expected|traceback|not equal|failure|failed|error:)`,
	},
	"compile-errors": {
		`(?i)(^#\s|:\d+:\d+:|:\d+:|undefined:|undeclared|syntax error|fatal error:|compilation terminated|build failed|cannot (find|use)|no required module provides package)`,
	},
	"git-status": {
		`^(##|[ MADRCU?!]{1,2}\s+)`,
	},
	"changed-files": {
		`^(?:[ MADRCU?!]{1,2}\s+\S|[ACDMRTUXB]\d*\s+\S|(?:\./)?[\w./-]+\.[A-Za-z0-9._-]+$|rename (?:from|to) |create mode |delete mode )`,
	},
	"summary-with-context": {
		`(?i)(^ok\b|^PASS\b|^FAIL\b|^Ran \d+ tests?|\bsummary\b|\btotal\b|\bfiles changed\b|\bchanged files\b|\bdone\b|\bfinished\b)`,
	},
}

func applyFilter(lines []string, filter Filter) ([]string, bool, error) {
	var err error
	filter, err = normalizeFilter(filter)
	if err != nil {
		return nil, false, err
	}
	if filter.Include == "" && filter.Exclude == "" && len(filter.Keywords) == 0 && len(filter.Regexes) == 0 {
		return nil, false, nil
	}
	include, err := compileFilterPattern(filter.Include, filter.CaseInsensitive)
	if err != nil {
		return nil, false, err
	}
	exclude, err := compileFilterPattern(filter.Exclude, filter.CaseInsensitive)
	if err != nil {
		return nil, false, err
	}
	regexes, err := compileFilterPatterns(filter.Regexes, filter.CaseInsensitive)
	if err != nil {
		return nil, false, err
	}
	selected := map[int]struct{}{}
	for i, line := range lines {
		if exclude != nil && exclude.MatchString(line) {
			continue
		}
		if include != nil && !include.MatchString(line) {
			continue
		}
		if len(filter.Keywords) > 0 && !matchesKeyword(line, filter.Keywords, filter.CaseInsensitive) {
			continue
		}
		if len(regexes) > 0 && !matchesRegexPack(line, regexes) {
			continue
		}
		start := max(0, i-filter.ContextBefore)
		end := min(len(lines)-1, i+filter.ContextAfter)
		for j := start; j <= end; j++ {
			selected[j] = struct{}{}
		}
	}
	indexes := make([]int, 0, len(selected))
	for index := range selected {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	truncated := false
	if filter.MaxLines > 0 && len(indexes) > filter.MaxLines {
		truncated = true
		indexes = indexes[:filter.MaxLines]
	}
	out := make([]string, 0, len(indexes))
	for _, index := range indexes {
		out = append(out, lines[index])
	}
	return out, truncated, nil
}

func normalizeFilter(filter Filter) (Filter, error) {
	if filter.Preset == "errors" {
		filter.Preset = "errors-only"
	}
	if !hasPositiveSelectors(filter) {
		switch filter.Preset {
		case "errors-only":
			filter.Packs = appendUniqueStrings(filter.Packs, "errors-only")
		case "tests":
			filter.Include = `(?i)(FAIL|PASS|RUN|panic|error|failed|---)`
		case "test-failures":
			filter.Packs = appendUniqueStrings(filter.Packs, "test-failures")
		case "compile-errors":
			filter.Packs = appendUniqueStrings(filter.Packs, "compile-errors")
		case "git-status":
			filter.Packs = appendUniqueStrings(filter.Packs, "git-status")
		case "changed-files":
			filter.Packs = appendUniqueStrings(filter.Packs, "changed-files")
		case "summary-with-context":
			filter.Packs = appendUniqueStrings(filter.Packs, "summary-with-context")
		}
	}
	if filter.Preset == "summary-with-context" {
		if filter.ContextBefore <= 0 {
			filter.ContextBefore = 1
		}
		if filter.ContextAfter <= 0 {
			filter.ContextAfter = 1
		}
	}
	expanded, err := expandRegexPacks(filter.Packs)
	if err != nil {
		return Filter{}, err
	}
	filter.Regexes = appendUniqueStrings(filter.Regexes, expanded...)
	if filter.MaxLines <= 0 {
		filter.MaxLines = 80
	}
	return filter, nil
}

func hasPositiveSelectors(filter Filter) bool {
	return filter.Include != "" || len(filter.Keywords) > 0 || len(filter.Regexes) > 0 || len(filter.Packs) > 0
}

func expandRegexPacks(packs []string) ([]string, error) {
	patterns := make([]string, 0)
	for _, pack := range packs {
		if strings.TrimSpace(pack) == "" {
			continue
		}
		values, ok := builtInRegexPacks[pack]
		if !ok {
			return nil, fmt.Errorf("unknown filter pack %q", pack)
		}
		patterns = append(patterns, values...)
	}
	return patterns, nil
}

func compileFilterPattern(pattern string, caseInsensitive bool) (*regexp.Regexp, error) {
	if pattern == "" {
		return nil, nil
	}
	if caseInsensitive && !strings.HasPrefix(pattern, "(?i)") {
		pattern = "(?i)" + pattern
	}
	return regexp.Compile(pattern)
}

func compileFilterPatterns(patterns []string, caseInsensitive bool) ([]*regexp.Regexp, error) {
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		re, err := compileFilterPattern(pattern, caseInsensitive)
		if err != nil {
			return nil, err
		}
		if re != nil {
			compiled = append(compiled, re)
		}
	}
	return compiled, nil
}

func matchesKeyword(line string, keywords []string, caseInsensitive bool) bool {
	candidate := line
	if caseInsensitive {
		candidate = strings.ToLower(candidate)
	}
	for _, keyword := range keywords {
		value := keyword
		if caseInsensitive {
			value = strings.ToLower(value)
		}
		if strings.Contains(candidate, value) {
			return true
		}
	}
	return false
}

func matchesRegexPack(line string, regexes []*regexp.Regexp) bool {
	for _, re := range regexes {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}

func appendUniqueStrings(base []string, values ...string) []string {
	seen := make(map[string]struct{}, len(base)+len(values))
	out := make([]string, 0, len(base)+len(values))
	for _, value := range base {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func normalizeLines(text string) []string {
	raw := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func tail80(values []string) []string {
	return tailN(values, 80)
}

func tailN(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return values[len(values)-limit:]
}

func shellBin() string {
	if runtime.GOOS == "windows" {
		return "cmd"
	}
	return "/bin/sh"
}

func shellArgs(cmd string) []string {
	if runtime.GOOS == "windows" {
		return []string{"/c", cmd}
	}
	return []string{"-lc", cmd}
}
