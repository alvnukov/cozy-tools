// Package config carries the host-injectable execution policy data and the
// value-resolution contract the command runner consumes. It is deliberately
// named config so ported call sites keep their config.CommandPolicy and
// config.DefaultConfigPath references unchanged; hosts alias their own
// configuration layer instead of importing this package.
package config

import "github.com/alvnukov/cozy-tools/security"

// CommandPolicy defines execution and output limits for the command runner.
// It is pure data: hosts construct it from their own configuration layer.
type CommandPolicy struct {
	DefaultTimeoutSeconds int      `yaml:"default_timeout_seconds" json:"default_timeout_seconds"`
	MaxOutputBytes        int      `yaml:"max_output_bytes" json:"max_output_bytes"`
	MaxLines              int      `yaml:"max_lines" json:"max_lines"`
	AllowedCWDs           []string `yaml:"allowed_cwds" json:"allowed_cwds"`
	LogDir                string   `yaml:"log_dir" json:"log_dir"`
	LogEnabled            *bool    `yaml:"log_enabled" json:"log_enabled"`
	LogRetentionDays      int      `yaml:"log_retention_days" json:"log_retention_days"`
	LogMaxRecords         int      `yaml:"log_max_records" json:"log_max_records"`
	LogCompress           bool     `yaml:"log_compress" json:"log_compress"`
	ProtectedConfigPath   string   `yaml:"-" json:"-"`
	// ProtectedMarkers are additional substrings that deny a command that
	// references them. They carry whatever the embedding host considers
	// untouchable state; the shared package contributes no markers of its
	// own.
	ProtectedMarkers []string `yaml:"-" json:"-"`
	// DenyMessage overrides the remediation text of protected-path
	// denials. Empty uses a host-neutral default that names no host
	// actions.
	DenyMessage string `yaml:"-" json:"-"`
}

// ResolvedValues carries the merged value channels for one execution: vars
// for template substitution, env pairs for the child process, and the secret
// mask for redaction.
type ResolvedValues struct {
	Vars map[string]string
	Env  []string
	Mask *security.Mask
}

// ValueResolver resolves secret handles and explicit vars and env into the
// values one execution consumes. The host implements it over its secret
// storage; the runner fails closed when a handle has no value.
type ValueResolver interface {
	ResolveValues(handles []string, explicitVars map[string]string, explicitEnv map[string]string) (ResolvedValues, error)
}

// CommandPolicy is pure data: hosts construct it from their own
// configuration layer and own every host-specific string in it. There is no
// package-level default path or mutable override hook: two runners built
// from two policies deny and report independently.
