package command

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alvnukov/cozy-tools/evidence"
	"github.com/alvnukov/cozy-tools/project"
)

const (
	defaultRetentionDays = 30
	defaultMaxRecords    = 2000
)

// HistoryPolicy controls command log persistence and cleanup.
type HistoryPolicy struct {
	Dir           string `yaml:"dir" json:"dir"`
	RetentionDays int    `yaml:"retention_days" json:"retention_days"`
	MaxRecords    int    `yaml:"max_records" json:"max_records"`
	Compress      bool   `yaml:"compress" json:"compress"`
}

// History stores bounded command output so callers can re-filter it later.
type History struct {
	mu       sync.RWMutex
	root     string
	policy   HistoryPolicy
	projects *project.Store
	records  map[string]Record
}

// Record is one retained command execution artifact.
type Record struct {
	CommandID   string    `json:"command_id"`
	Status      string    `json:"status"`
	RepoPath    string    `json:"repo_path"`
	Command     string    `json:"command"`
	CWD         string    `json:"cwd"`
	ExitCode    int       `json:"exit_code"`
	DurationMS  int64     `json:"duration_ms,omitempty"`
	Truncated   bool      `json:"truncated"`
	Stdout      []string  `json:"stdout"`
	Stderr      []string  `json:"stderr"`
	Combined    []string  `json:"combined"`
	OutputHash  string    `json:"output_hash"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type indexEntry struct {
	CommandID  string    `json:"command_id"`
	Status     string    `json:"status"`
	RepoPath   string    `json:"repo_path"`
	Project    string    `json:"project"`
	Command    string    `json:"command"`
	CWD        string    `json:"cwd"`
	ExitCode   int       `json:"exit_code"`
	Truncated  bool      `json:"truncated"`
	OutputHash string    `json:"output_hash"`
	CreatedAt  time.Time `json:"created_at"`
	File       string    `json:"file"`
	IndexPath  string    `json:"-"`
}

// NewHistory creates a persistent command history under policy.Dir.
func NewHistory(policy HistoryPolicy) (*History, error) {
	policy = normalizeHistoryPolicy(policy)
	projects, err := project.NewStore(policy.Dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(projects.Root(), "repos"), 0o700); err != nil {
		return nil, fmt.Errorf("create helper repo directory: %w", err)
	}
	history := &History{
		root:     projects.Root(),
		policy:   policy,
		projects: projects,
		records:  map[string]Record{},
	}
	if err := history.loadIndex(); err != nil {
		return nil, err
	}
	if err := history.Cleanup(); err != nil {
		return nil, err
	}
	return history, nil
}

// NewInMemoryHistory creates a volatile fallback history.
func NewInMemoryHistory() *History {
	return &History{policy: HistoryPolicy{}, records: map[string]Record{}}
}

// Put stores record in memory and on disk.
func (h *History) Put(record Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if strings.TrimSpace(record.CommandID) == "" {
		return errors.New("command_id is required")
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	if h.root == "" {
		h.records[record.CommandID] = record
		return nil
	}
	logsDir, projectName, err := h.logsDir(record.RepoPath)
	if err != nil {
		return err
	}
	recordsDir := filepath.Join(logsDir, "records")
	if err := os.MkdirAll(recordsDir, 0o700); err != nil {
		return fmt.Errorf("create command log record directory: %w", err)
	}
	fileName := record.CommandID + ".json"
	if h.policy.Compress {
		fileName += ".gz"
	}
	recordPath := filepath.Join(recordsDir, fileName)
	if err := writeJSONRecord(recordPath, record, h.policy.Compress); err != nil {
		return err
	}
	entry := indexEntry{
		CommandID:  record.CommandID,
		Status:     record.Status,
		RepoPath:   record.RepoPath,
		Project:    projectName,
		Command:    record.Command,
		CWD:        record.CWD,
		ExitCode:   record.ExitCode,
		Truncated:  record.Truncated,
		OutputHash: record.OutputHash,
		CreatedAt:  record.CreatedAt,
		File:       recordPath,
	}
	// The append runs under the inter-process lock: a cleanup rewrite in
	// another helper renames the index out from under an in-flight append,
	// and the row it was writing would be lost to the discarded inode.
	lock, err := acquireIndexLock(h.root)
	if err != nil {
		return err
	}
	err = appendIndexEntry(filepath.Join(logsDir, "index.jsonl"), entry)
	lock.release()
	if err != nil {
		return err
	}
	h.records[record.CommandID] = record
	return nil
}

// Filter returns a previously executed command record with a new output filter applied.
func (h *History) Filter(commandID string, filter Filter) (Result, error) {
	record, ok, err := h.getRecord(commandID)
	if err != nil {
		return Result{}, err
	}
	if !ok {
		return Result{}, fmt.Errorf("command history entry %q not found", commandID)
	}
	filteredLines, filterTruncated, err := applyFilter(record.Combined, filter)
	if err != nil {
		return Result{}, err
	}
	durationMS := record.DurationMS
	if record.Status == "running" && !record.StartedAt.IsZero() {
		durationMS = time.Since(record.StartedAt).Milliseconds()
	}
	evidenceLines, evidenceDistilled := evidenceFromLines(record.Combined)
	return Result{
		Status:        record.Status,
		CommandID:     commandID,
		Command:       record.Command,
		CWD:           record.CWD,
		ExitCode:      record.ExitCode,
		DurationMS:    durationMS,
		Truncated:     record.Truncated || filterTruncated,
		StdoutTail:    tail80(record.Stdout),
		StderrTail:    tail80(record.Stderr),
		FilteredLines: filteredLines,
		EvidenceLines: evidenceLines,
		OutputHash:    record.OutputHash,
		NextCall:      nextCallForStatus(record.Status, commandID),

		FailureMarkers:    maskedFailureMarkers(record.ExitCode, record.Combined),
		EvidenceDistilled: evidenceDistilled,
	}, nil
}

// UpdateRunningOutput refreshes the in-memory output snapshot for a running command.
// Persistent records are finalized once the command exits; live inspection only needs
// the current process-local snapshot.
func (h *History) UpdateRunningOutput(commandID string, stdoutLines []string, stderrLines []string, combined []string, truncated bool, outputHash string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	record, ok := h.records[commandID]
	if !ok {
		return fmt.Errorf("command %q not found in history", commandID)
	}
	if record.Status != "running" {
		return nil
	}
	record.Stdout = append([]string(nil), stdoutLines...)
	record.Stderr = append([]string(nil), stderrLines...)
	record.Combined = append([]string(nil), combined...)
	record.Truncated = truncated
	record.OutputHash = outputHash
	if !record.StartedAt.IsZero() {
		record.DurationMS = time.Since(record.StartedAt).Milliseconds()
	}
	h.records[commandID] = record
	return nil
}

// getRecord returns the best record known for commandID.
//
// A terminal record never changes again, so a cached copy of one is
// authoritative and is returned as it stands. A running record is not a fact but
// a claim — that the command had not finished the last time this process looked
// at it. The process that finishes it need not be this one: several helpers can
// share a log directory, and even within one helper the command outlives the MCP
// call that started it. Serving that claim from memory is what left a command
// that had exited reporting itself as running until the helper restarted, with
// its output already on disk and unreachable.
//
// So a cached running record is checked against the durable index before it is
// believed, and a terminal record found there replaces it.
func (h *History) getRecord(commandID string) (Record, bool, error) {
	h.mu.RLock()
	cached, cachedOK := h.records[commandID]
	h.mu.RUnlock()
	if h.root == "" || (cachedOK && isTerminalStatus(cached.Status)) {
		return cached, cachedOK, nil
	}

	durable, durableOK, err := h.durableRecord(commandID)
	if err != nil {
		return Record{}, false, err
	}
	if durableOK && isTerminalStatus(durable.Status) {
		h.mu.Lock()
		h.records[commandID] = durable
		h.mu.Unlock()
		return durable, true, nil
	}
	if cachedOK {
		// Still running as far as anyone knows. The local snapshot is preferred
		// because it carries the output streamed so far, which the durable
		// record does not hold until the command exits.
		return cached, true, nil
	}
	return durable, durableOK, nil
}

// durableRecord reads one command's record from disk.
//
// It resolves the entry itself rather than calling loadIndex, because a caller
// polling a running command would otherwise decompress every record the helper
// has ever written, once per poll.
func (h *History) durableRecord(commandID string) (Record, bool, error) {
	entries, err := h.readEntries()
	if err != nil {
		return Record{}, false, err
	}
	var best indexEntry
	found := false
	for _, entry := range entries {
		if entry.CommandID != commandID {
			continue
		}
		if !found || supersedes(entry, best) {
			best, found = entry, true
		}
	}
	if !found {
		return Record{}, false, nil
	}
	record, err := readJSONRecord(best.File)
	if err != nil {
		// A record file that cannot be read is reported as absent rather than as
		// an error: the caller's next best answer is whatever it already had.
		return Record{}, false, nil
	}
	return record, true, nil
}

// isTerminalStatus reports whether a status is one a command cannot leave.
func isTerminalStatus(status string) bool {
	return status != "" && status != "running"
}

// supersedes reports whether candidate is the better index row for a command
// that has more than one.
//
// A terminal row always wins over a running one, whatever the two timestamps
// say: the running row is written before the command starts and only records an
// intention, while the terminal row is written from its result.
func supersedes(candidate indexEntry, current indexEntry) bool {
	if isTerminalStatus(candidate.Status) != isTerminalStatus(current.Status) {
		return isTerminalStatus(candidate.Status)
	}
	return candidate.CreatedAt.After(current.CreatedAt)
}

// latestEntries collapses an index to one row per command.
//
// Every command writes at least twice — once when it starts and once when it
// ends — and the index is append-only, so a reader that treats rows as commands
// sees each finished command twice: once correctly, and once as a run that never
// came back.
func latestEntries(entries []indexEntry) []indexEntry {
	best := make(map[string]indexEntry, len(entries))
	order := make([]string, 0, len(entries))
	for _, entry := range entries {
		current, seen := best[entry.CommandID]
		if !seen {
			order = append(order, entry.CommandID)
			best[entry.CommandID] = entry
			continue
		}
		if supersedes(entry, current) {
			best[entry.CommandID] = entry
		}
	}
	collapsed := make([]indexEntry, 0, len(order))
	for _, commandID := range order {
		collapsed = append(collapsed, best[commandID])
	}
	return collapsed
}

// lastIdenticalRun returns the newest completed run of command in repoPath,
// ignoring the run identified by exceptID.
//
// Only that repository's own index is read. A repeat is only meaningful against
// the tree it ran on, and scanning every project's index on every command would
// make the lookup grow with the helper's entire history.
func (h *History) lastIdenticalRun(repoPath string, command string, exceptID string) (indexEntry, bool) {
	if h.root == "" {
		return h.lastIdenticalCached(repoPath, command, exceptID)
	}
	logsDir, _, err := h.logsDir(repoPath)
	if err != nil {
		return indexEntry{}, false
	}
	entries, err := readEntriesFile(filepath.Join(logsDir, "index.jsonl"))
	if err != nil {
		return indexEntry{}, false
	}
	var best indexEntry
	found := false
	for _, entry := range latestEntries(entries) {
		if entry.CommandID == exceptID || entry.Command != command {
			continue
		}
		if !isTerminalStatus(entry.Status) {
			continue
		}
		if !found || entry.CreatedAt.After(best.CreatedAt) {
			best, found = entry, true
		}
	}
	return best, found
}

// lastIdenticalCached answers the same question for a history with no disk root.
func (h *History) lastIdenticalCached(repoPath string, command string, exceptID string) (indexEntry, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var best indexEntry
	found := false
	for id, record := range h.records {
		if id == exceptID || record.Command != command || record.RepoPath != repoPath {
			continue
		}
		if !isTerminalStatus(record.Status) {
			continue
		}
		entry := indexEntry{
			CommandID:  record.CommandID,
			Status:     record.Status,
			RepoPath:   record.RepoPath,
			Command:    record.Command,
			ExitCode:   record.ExitCode,
			OutputHash: record.OutputHash,
			CreatedAt:  record.CreatedAt,
		}
		if !found || entry.CreatedAt.After(best.CreatedAt) {
			best, found = entry, true
		}
	}
	return best, found
}

// Cleanup removes records outside retention policy and rewrites the search indexes.
// The snapshot-through-rewrite section runs under the inter-process index
// lock, so rows another helper appends mid-cleanup survive the rewrite.
func (h *History) Cleanup() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.root == "" {
		return nil
	}
	// The lock spans the snapshot read through the index rewrites: a terminal
	// row another helper appends in that window would otherwise land in the
	// inode the rename discards, leaving its command "running" for as long as
	// the shared log root lives. Appends take the same lock, so the window is
	// closed by mutual exclusion rather than by timing.
	lock, err := acquireIndexLock(h.root)
	if err != nil {
		return err
	}
	defer lock.release()
	entries, err := h.readEntries()
	if err != nil {
		return err
	}
	// Retention counts commands, not index rows. Every command has at least two
	// rows and they all name the same record file, so dropping a superseded
	// running row would delete the file its own terminal row still points at —
	// and MaxRecords would budget for half the history it appears to. Collapsing
	// first fixes both, and rewriting the index leaves it collapsed for good.
	commands := latestEntries(entries)
	cutoff := time.Now().UTC().AddDate(0, 0, -h.policy.RetentionDays)
	kept := make([]indexEntry, 0, len(commands))
	dropped := make([]indexEntry, 0, len(commands))
	for _, entry := range commands {
		if entry.CreatedAt.Before(cutoff) {
			dropped = append(dropped, entry)
			continue
		}
		kept = append(kept, entry)
	}
	sort.Slice(kept, func(i, j int) bool {
		return kept[i].CreatedAt.Before(kept[j].CreatedAt)
	})
	if h.policy.MaxRecords > 0 && len(kept) > h.policy.MaxRecords {
		drop := len(kept) - h.policy.MaxRecords
		dropped = append(dropped, kept[:drop]...)
		kept = kept[drop:]
	}
	for _, entry := range dropped {
		h.removeEntry(entry)
	}
	return rewriteIndexes(entries, kept)
}

// ListRequest controls which command records to return.
type ListRequest struct {
	Status   string `json:"status,omitempty"`
	RepoPath string `json:"repo_path,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

// ListEntry is a compact summary of one command record for listing.
type ListEntry struct {
	CommandID  string    `json:"command_id"`
	Status     string    `json:"status"`
	RepoPath   string    `json:"repo_path,omitempty"`
	Command    string    `json:"command"`
	CWD        string    `json:"cwd"`
	ExitCode   int       `json:"exit_code"`
	DurationMS int64     `json:"duration_ms,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// ListResult holds the bounded result of a command list operation.
type ListResult struct {
	Entries []ListEntry `json:"entries"`
	Total   int         `json:"total"`
}

// List returns a bounded, sorted (newest-first) list of command records.
func (h *History) List(req ListRequest) (ListResult, error) {
	if req.Limit <= 0 {
		req.Limit = 50
	}
	if req.Limit > 200 {
		req.Limit = 200
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.root == "" {
		// In-memory fallback: iterate records map.
		entries := make([]ListEntry, 0, len(h.records))
		for _, r := range h.records {
			if !matchesFilter(r, req) {
				continue
			}
			entries = append(entries, recordToEntry(r))
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].CreatedAt.After(entries[j].CreatedAt)
		})
		total := len(entries)
		if len(entries) > req.Limit {
			entries = entries[:req.Limit]
		}
		return ListResult{Entries: entries, Total: total}, nil
	}
	// Persistent history: read index entries, collapsed to one row per command
	// so a finished command is listed once, by how it finished, instead of twice
	// — the second time as a run that never came back.
	idxEntries, err := h.readEntries()
	if err != nil {
		return ListResult{}, err
	}
	idxEntries = latestEntries(idxEntries)
	entries := make([]ListEntry, 0, len(idxEntries))
	for _, e := range idxEntries {
		if req.Status != "" && e.Status != req.Status {
			continue
		}
		if req.RepoPath != "" && e.RepoPath != req.RepoPath {
			continue
		}
		entries = append(entries, ListEntry{
			CommandID: e.CommandID,
			Status:    e.Status,
			RepoPath:  e.RepoPath,
			Command:   e.Command,
			CWD:       e.CWD,
			ExitCode:  e.ExitCode,
			CreatedAt: e.CreatedAt,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].CreatedAt.After(entries[j].CreatedAt)
	})
	total := len(entries)
	if len(entries) > req.Limit {
		entries = entries[:req.Limit]
	}
	return ListResult{Entries: entries, Total: total}, nil
}

func matchesFilter(r Record, req ListRequest) bool {
	if req.Status != "" && r.Status != req.Status {
		return false
	}
	if req.RepoPath != "" && r.RepoPath != req.RepoPath {
		return false
	}
	return true
}

func recordToEntry(r Record) ListEntry {
	e := ListEntry{
		CommandID:  r.CommandID,
		Status:     r.Status,
		RepoPath:   r.RepoPath,
		Command:    r.Command,
		CWD:        r.CWD,
		ExitCode:   r.ExitCode,
		DurationMS: r.DurationMS,
		CreatedAt:  r.CreatedAt,
	}
	if r.Status == "running" && !r.StartedAt.IsZero() {
		e.DurationMS = time.Since(r.StartedAt).Milliseconds()
	}
	return e
}

// UpdateStatus changes the status of a record in memory.
// For persistent history, the on-disk record is not rewritten (status is cosmetic for abort).
func (h *History) UpdateStatus(commandID string, status string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	record, ok := h.records[commandID]
	if !ok {
		return fmt.Errorf("command %q not found in history", commandID)
	}
	record.Status = status
	h.records[commandID] = record
	return nil
}

func (h *History) logsDir(repoPath string) (string, string, error) {
	if strings.TrimSpace(repoPath) == "" {
		return filepath.Join(h.root, "logs"), "", nil
	}
	name, err := project.Name(repoPath)
	if err != nil {
		return "", "", err
	}
	logsDir, err := h.projects.LogsDir(repoPath)
	if err != nil {
		return "", "", err
	}
	return logsDir, name, nil
}

func (h *History) removeEntry(entry indexEntry) {
	_ = os.Remove(entry.File)
	delete(h.records, entry.CommandID)
}

func (h *History) loadIndex() error {
	entries, err := h.readEntries()
	if err != nil {
		return err
	}
	for _, entry := range latestEntries(entries) {
		record, err := readJSONRecord(entry.File)
		if err != nil {
			continue
		}
		if _, cached := h.records[entry.CommandID]; cached && !isTerminalStatus(record.Status) {
			// Anything already in memory is at least as fresh as a running
			// record on disk, and may hold output streamed since it was written.
			continue
		}
		h.records[entry.CommandID] = record
	}
	return nil
}

func (h *History) readEntries() ([]indexEntry, error) {
	indexPaths, err := h.indexPaths()
	if err != nil {
		return nil, err
	}
	var entries []indexEntry
	for _, indexPath := range indexPaths {
		fileEntries, err := readEntriesFile(indexPath)
		if err != nil {
			return nil, err
		}
		entries = append(entries, fileEntries...)
	}
	return entries, nil
}

func (h *History) indexPaths() ([]string, error) {
	pattern := filepath.Join(h.root, "repos", "*", "logs", "index.jsonl")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("scan command log indexes: %w", err)
	}
	legacy := filepath.Join(h.root, "logs", "index.jsonl")
	if _, err := os.Stat(legacy); err == nil {
		paths = append(paths, legacy)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("stat legacy command log index: %w", err)
	}
	return paths, nil
}

func readEntriesFile(indexPath string) ([]indexEntry, error) {
	// #nosec G304 -- indexPath is discovered under the configured helper log root.
	file, err := os.Open(indexPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("open command log index: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()
	var entries []indexEntry
	// Put embeds the full command string in each line, so lines have no
	// size ceiling; bufio.Reader grows past the Scanner's 64KB token limit.
	reader := bufio.NewReader(file)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			if idx := bytes.LastIndexByte(line, '\n'); idx >= 0 {
				line = line[:idx]
			}
			if len(line) > 0 {
				var entry indexEntry
				if err := json.Unmarshal(line, &entry); err != nil {
					return nil, fmt.Errorf("decode command log index: %w", err)
				}
				entry.IndexPath = indexPath
				recordPath, err := normalizeRecordPath(indexPath, entry.File)
				if err != nil {
					return nil, fmt.Errorf("decode command log index: %w", err)
				}
				entry.File = recordPath
				entries = append(entries, entry)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return nil, fmt.Errorf("scan command log index: %w", readErr)
		}
	}
	return entries, nil
}

func normalizeRecordPath(indexPath string, recordPath string) (string, error) {
	if strings.TrimSpace(recordPath) == "" {
		return "", errors.New("command log record path is empty")
	}
	candidate := filepath.FromSlash(recordPath)
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(filepath.Dir(indexPath), candidate)
	}
	candidate = filepath.Clean(candidate)
	recordsDir := filepath.Join(filepath.Dir(indexPath), "records")
	relative, err := filepath.Rel(recordsDir, candidate)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.Dir(relative) != "." {
		return "", fmt.Errorf("command log record path escapes records directory: %q", recordPath)
	}
	if !strings.HasSuffix(relative, ".json") && !strings.HasSuffix(relative, ".json.gz") {
		return "", fmt.Errorf("command log record has unsupported extension: %q", recordPath)
	}
	return candidate, nil
}

func appendIndexEntry(path string, entry indexEntry) error {
	// #nosec G304 -- path is derived from the configured command log directory and normalized record id.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open command log index: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()
	entry.IndexPath = ""
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write command log index: %w", err)
	}
	return nil
}

func rewriteIndexes(oldEntries []indexEntry, keptEntries []indexEntry) error {
	grouped := map[string][]indexEntry{}
	for _, entry := range oldEntries {
		grouped[entry.IndexPath] = nil
	}
	for _, entry := range keptEntries {
		grouped[entry.IndexPath] = append(grouped[entry.IndexPath], entry)
	}
	for indexPath, entries := range grouped {
		if err := rewriteIndex(indexPath, entries); err != nil {
			return err
		}
	}
	return nil
}

func rewriteIndex(path string, entries []indexEntry) error {
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("open temporary command log index: %w", err)
	}
	tmpPath := file.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpPath)
		}
	}()
	for _, entry := range entries {
		entry.IndexPath = ""
		data, err := json.Marshal(entry)
		if err != nil {
			_ = file.Close()
			return err
		}
		if _, err := file.Write(append(data, '\n')); err != nil {
			_ = file.Close()
			return fmt.Errorf("write temporary command log index: %w", err)
		}
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync temporary command log index: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temporary command log index: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace command log index: %w", err)
	}
	removeTemp = false
	return nil
}

func writeJSONRecord(path string, record Record, compress bool) error {
	// #nosec G304 -- path is derived from the configured command log directory and normalized record id.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open command log record: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()
	if compress {
		writer := gzip.NewWriter(file)
		if err := json.NewEncoder(writer).Encode(record); err != nil {
			_ = writer.Close()
			return fmt.Errorf("encode compressed command log record: %w", err)
		}
		if err := writer.Close(); err != nil {
			return fmt.Errorf("close compressed command log record: %w", err)
		}
		return nil
	}
	if err := json.NewEncoder(file).Encode(record); err != nil {
		return fmt.Errorf("encode command log record: %w", err)
	}
	return nil
}

const maxHistoryRecordBytes = 16 << 20

func readJSONRecord(path string) (Record, error) {
	// #nosec G304 -- path is validated against the command log records directory before this call.
	file, err := os.Open(path)
	if err != nil {
		return Record{}, fmt.Errorf("open command log record: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()
	var reader io.Reader = file
	if strings.HasSuffix(path, ".gz") {
		gzipReader, err := gzip.NewReader(file)
		if err != nil {
			return Record{}, fmt.Errorf("open compressed command log record: %w", err)
		}
		defer func() {
			_ = gzipReader.Close()
		}()
		reader = gzipReader
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxHistoryRecordBytes+1))
	if err != nil {
		return Record{}, fmt.Errorf("read command log record: %w", err)
	}
	if len(data) > maxHistoryRecordBytes {
		return Record{}, fmt.Errorf("command log record exceeds %d byte limit", maxHistoryRecordBytes)
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, fmt.Errorf("decode command log record: %w", err)
	}
	return record, nil
}

func evidenceFromLines(lines []string) ([]evidence.Line, bool) {
	return evidence.SelectDistilled(lines, 30)
}

func normalizeHistoryPolicy(policy HistoryPolicy) HistoryPolicy {
	home, homeErr := os.UserHomeDir()
	if policy.Dir == "" {
		if homeErr == nil {
			policy.Dir = filepath.Join(home, ".mcp-ai-helper")
		} else {
			policy.Dir = filepath.Join(".", ".mcp-ai-helper")
		}
	} else if strings.HasPrefix(policy.Dir, "~/") && homeErr == nil {
		policy.Dir = filepath.Join(home, strings.TrimPrefix(policy.Dir, "~/"))
	}
	if policy.RetentionDays <= 0 {
		policy.RetentionDays = defaultRetentionDays
	}
	if policy.MaxRecords <= 0 {
		policy.MaxRecords = defaultMaxRecords
	}
	return policy
}
