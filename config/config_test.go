package config

import (
	"testing"

	"github.com/alvnukov/cozy-tools/security"
)

func TestDefaultConfigPathDefaultsEmpty(t *testing.T) {
	if DefaultConfigPath() != "" {
		t.Fatalf("DefaultConfigPath() = %q, want empty default", DefaultConfigPath())
	}
}

func TestDefaultConfigPathHostOverride(t *testing.T) {
	restore := DefaultConfigPathFn
	defer func() { DefaultConfigPathFn = restore }()
	DefaultConfigPathFn = func() string { return "/etc/host/config.yaml" }
	if got := DefaultConfigPath(); got != "/etc/host/config.yaml" {
		t.Fatalf("DefaultConfigPath() = %q, want /etc/host/config.yaml", got)
	}
}

// fakeResolver proves the ValueResolver seam stays implementable by hosts:
// a minimal adapter satisfies it without importing anything beyond the mask.
type fakeResolver struct{}

func (fakeResolver) ResolveValues(handles []string, explicitVars map[string]string, explicitEnv map[string]string) (ResolvedValues, error) {
	return ResolvedValues{Vars: explicitVars, Env: []string{"K=V"}, Mask: security.NewMask()}, nil
}

var _ ValueResolver = fakeResolver{}

func TestResolvedValuesCarriesMaskAndEnv(t *testing.T) {
	got, err := fakeResolver{}.ResolveValues(nil, map[string]string{"A": "1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Vars["A"] != "1" || len(got.Env) != 1 || got.Mask == nil {
		t.Fatalf("ResolvedValues = %+v", got)
	}
}
