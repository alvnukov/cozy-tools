package tool

import (
	"fmt"
	"slices"
)

// Effect describes an externally observable capability used by an operation.
type Effect string

const (
	EffectFilesystemRead    Effect = "filesystem.read"
	EffectFilesystemWrite   Effect = "filesystem.write"
	EffectProcessExecute    Effect = "process.execute"
	EffectProcessBackground Effect = "process.background"
	EffectNetworkAccess     Effect = "network.access"
	EffectGitRead           Effect = "git.read"
	EffectGitWrite          Effect = "git.write"
	EffectConfigRead        Effect = "config.read"
	EffectConfigWrite       Effect = "config.write"
	EffectMemoryRead        Effect = "memory.read"
	EffectMemoryWrite       Effect = "memory.write"
	EffectModelInvoke       Effect = "model.invoke"
	EffectUserInteraction   Effect = "user.interaction"
	EffectHostLifecycle     Effect = "host.lifecycle"
)

var knownEffects = map[Effect]struct{}{
	EffectFilesystemRead: {}, EffectFilesystemWrite: {},
	EffectProcessExecute: {}, EffectProcessBackground: {},
	EffectNetworkAccess: {}, EffectGitRead: {}, EffectGitWrite: {},
	EffectConfigRead: {}, EffectConfigWrite: {},
	EffectMemoryRead: {}, EffectMemoryWrite: {},
	EffectModelInvoke: {}, EffectUserInteraction: {}, EffectHostLifecycle: {},
}

// EffectSet is a duplicate-free set with stable declaration order.
type EffectSet []Effect

// NewEffectSet validates effects and removes duplicates while preserving order.
func NewEffectSet(effects ...Effect) (EffectSet, error) {
	set := make(EffectSet, 0, len(effects))
	seen := make(map[Effect]struct{}, len(effects))
	for _, effect := range effects {
		if _, ok := knownEffects[effect]; !ok {
			return nil, fmt.Errorf("unknown effect %q", effect)
		}
		if _, ok := seen[effect]; ok {
			continue
		}
		seen[effect] = struct{}{}
		set = append(set, effect)
	}
	return set, nil
}

// MustEffects is NewEffectSet for declarations.
func MustEffects(effects ...Effect) EffectSet {
	set, err := NewEffectSet(effects...)
	if err != nil {
		panic(err)
	}
	return set
}

// Contains reports whether effect is present.
func (s EffectSet) Contains(effect Effect) bool { return slices.Contains(s, effect) }

func (s EffectSet) clone() EffectSet { return slices.Clone(s) }

func (s EffectSet) validate() error {
	seen := make(map[Effect]struct{}, len(s))
	for _, effect := range s {
		if _, ok := knownEffects[effect]; !ok {
			return fmt.Errorf("unknown effect %q", effect)
		}
		if _, ok := seen[effect]; ok {
			return fmt.Errorf("duplicate effect %q", effect)
		}
		seen[effect] = struct{}{}
	}
	return nil
}

// Mutates reports whether the set contains an effect that can change local or
// remote state without further interpretation of its arguments.
func (s EffectSet) Mutates() bool {
	for _, effect := range s {
		switch effect {
		case EffectFilesystemWrite,
			EffectProcessExecute,
			EffectProcessBackground,
			EffectGitWrite,
			EffectConfigWrite,
			EffectMemoryWrite,
			EffectHostLifecycle:
			return true
		}
	}
	return false
}

// Hints describe behavioral properties used by transports such as MCP.
// Effects remain the authoritative input to permission policy.
type Hints struct {
	ReadOnly    bool
	Destructive bool
	Idempotent  bool
	OpenWorld   bool
}

// Operation describes one permission-relevant operation of a tool.
type Operation struct {
	Name    string
	Effects EffectSet
	Hints   Hints
}

func (o Operation) clone() Operation {
	o.Effects = o.Effects.clone()
	return o
}
