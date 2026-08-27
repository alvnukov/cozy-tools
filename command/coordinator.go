package command

import (
	"path/filepath"
	"reflect"
	"sort"
	"sync"

	"github.com/alvnukov/cozy-tools/config"
	"github.com/alvnukov/cozy-tools/security"
)

// Coordinator owns the command lifecycle across the base policy and per-repo
// effective policies. Repo-local execution used to build a fresh Runner per
// call while get/list/abort consulted the base Runner, so a command started
// under repo-local config had no owner among the public command actions:
// abort answered not_found or waited forever on a pending publication, and a
// runner with private history was never polled to its final state.
//
// One Runner per repository, rebuilt only when the effective policy changes,
// keeps active-process ownership, live output, abort and durable history with
// a single owner per command id.
type Coordinator struct {
	base *Runner

	mu      sync.Mutex
	current map[string]*Runner
	retired []*Runner
}

// NewCoordinator creates a coordinator over the base-policy runner.
func NewCoordinator(base *Runner) *Coordinator {
	return &Coordinator{base: base, current: make(map[string]*Runner)}
}

// ForRepo returns the runner executing under repoPath's effective policy.
// The cached runner is reused while the policy is unchanged; a policy change
// builds a replacement and retires the previous runner, which stays reachable
// through Resolve for the command ids it still owns.
func (c *Coordinator) ForRepo(repoPath string, policy config.CommandPolicy, mask *security.Mask) *Runner {
	key := filepath.Clean(repoPath)
	c.mu.Lock()
	defer c.mu.Unlock()
	if runner, ok := c.current[key]; ok && reflect.DeepEqual(runner.policy, policy) {
		return runner
	}
	runner := NewRunnerWithMask(policy, mask)
	if previous, ok := c.current[key]; ok {
		c.retired = append(c.retired, previous)
	}
	c.current[key] = runner
	return runner
}

// Reset swaps the base runner and drops the per-repo registry, whose
// effective policies derive from the previous server config. It belongs to
// config reload.
func (c *Coordinator) Reset(base *Runner) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.base = base
	c.current = make(map[string]*Runner)
	c.retired = nil
}

// Resolve returns the runner that owns commandID: the one tracking it as an
// active process wins, then the first holding its durable record. Unknown ids
// fall back to the base runner, which answers with the same not_found a
// single-runner deployment would give.
func (c *Coordinator) Resolve(commandID string) *Runner {
	runners := c.snapshot()
	for _, runner := range runners {
		if runner.tracksCommand(commandID) {
			return runner
		}
	}
	for _, runner := range runners {
		if runner.knowsCommand(commandID) {
			return runner
		}
	}
	return c.base
}

// snapshot copies the resolution order under one lock: base first, then the
// current runner of each repository in stable order, then retired runners.
func (c *Coordinator) snapshot() []*Runner {
	c.mu.Lock()
	defer c.mu.Unlock()
	runners := []*Runner{c.base}
	keys := make([]string, 0, len(c.current))
	for key := range c.current {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		runners = append(runners, c.current[key])
	}
	return append(runners, c.retired...)
}
