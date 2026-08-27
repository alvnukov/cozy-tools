// Package security provides secret masking for MCP responses.
package security

import (
	"strings"
	"sync"
)

// namedSecret pairs a secret value with its handle name for precise masking.
type namedSecret struct {
	value string
	name  string
}

// Mask holds sensitive strings that must be redacted from output.
type Mask struct {
	mu      sync.RWMutex
	secrets []namedSecret
}

// NewMask creates an empty Mask.
func NewMask() *Mask {
	return &Mask{}
}

// AddNamed adds a secret value with its handle name.
func (m *Mask) AddNamed(name, value string) {
	if value == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.secrets = append(m.secrets, namedSecret{value: value, name: name})
}

// Add adds a secret value with an empty name for backward compatibility.
func (m *Mask) Add(value string) {
	m.AddNamed("", value)
}

// Apply replaces all known secrets in s with their named mask token.
func (m *Mask) Apply(s string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, sec := range m.secrets {
		if sec.value != "" && strings.Contains(s, sec.value) {
			repl := "***"
			if sec.name != "" {
				repl = "[HELPER_SECRET:" + sec.name + "]"
			}
			s = strings.ReplaceAll(s, sec.value, repl)
		}
	}
	return s
}
