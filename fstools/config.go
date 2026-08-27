package fstools

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/alvnukov/cozy-tools/filesystem"
	"github.com/alvnukov/cozy-tools/tool"
)

const (
	// TagLength is the number of leading SHA-256 characters rendered in native
	// @file headers.
	TagLength         = 8
	defaultModelBytes = 50 << 10
	maxReadManyFiles  = 32
)

// RootCapability resolves and authorizes a requested host root. Native tools
// always request ""; MCP tools pass their repo_path unchanged.
type RootCapability interface {
	ResolveRoot(context.Context, string) (string, error)
}

// RootCapabilityFunc adapts a function to RootCapability.
type RootCapabilityFunc func(context.Context, string) (string, error)

// ResolveRoot implements RootCapability.
func (f RootCapabilityFunc) ResolveRoot(ctx context.Context, requested string) (string, error) {
	if f == nil {
		return "", errors.New("nil root capability function")
	}
	return f(ctx, requested)
}

// FixedRoot is a RootCapability that authorizes every request to one fixed
// local path. It is useful for single-workspace hosts and tests.
type FixedRoot string

// ResolveRoot implements RootCapability.
func (root FixedRoot) ResolveRoot(ctx context.Context, _ string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return string(root), nil
}

// Config configures filesystem catalogs. Limits are the caps passed to every
// short-lived filesystem.Service. ModelBytes bounds MCP structured results and
// native grep rendering; zero selects 50 KiB.
type Config struct {
	RootCapability RootCapability
	Limits         filesystem.Limits
	ModelBytes     int
}

type adapter struct {
	roots      RootCapability
	limits     filesystem.Limits
	modelBytes int
}

func newAdapter(config Config) (adapter, error) {
	if isNilCapability(config.RootCapability) {
		return adapter{}, tool.NewError(tool.CodePermissionDenied, "filesystem root capability is required")
	}
	if config.ModelBytes < 0 {
		return adapter{}, tool.NewError(tool.CodeInvalidArgument, "model byte budget must not be negative")
	}
	modelBytes := config.ModelBytes
	if modelBytes == 0 {
		modelBytes = defaultModelBytes
	}
	if modelBytes < 1024 {
		return adapter{}, tool.NewError(tool.CodeInvalidArgument, "model byte budget must be at least 1024 bytes")
	}
	if err := validateLimits(config.Limits); err != nil {
		return adapter{}, err
	}
	return adapter{roots: config.RootCapability, limits: config.Limits, modelBytes: modelBytes}, nil
}

func isNilCapability(capability RootCapability) bool {
	if capability == nil {
		return true
	}
	value := reflect.ValueOf(capability)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func validateLimits(limits filesystem.Limits) error {
	defaults := filesystem.DefaultLimits()
	checks := []struct {
		name string
		got  int64
		max  int64
	}{
		{"whole_file_bytes", limits.WholeFileBytes, defaults.WholeFileBytes},
		{"read_lines", int64(limits.ReadLines), int64(defaults.ReadLines)},
		{"read_bytes", limits.ReadBytes, defaults.ReadBytes},
		{"list_entries", int64(limits.ListEntries), int64(defaults.ListEntries)},
		{"find_matches", int64(limits.FindMatches), int64(defaults.FindMatches)},
		{"search_matches", int64(limits.SearchMatches), int64(defaults.SearchMatches)},
		{"search_bytes", limits.SearchBytes, defaults.SearchBytes},
		{"scan_files", int64(limits.ScanFiles), int64(defaults.ScanFiles)},
		{"scan_bytes", limits.ScanBytes, defaults.ScanBytes},
	}
	for _, check := range checks {
		if check.got < 0 || check.got > check.max {
			return tool.NewError(tool.CodeInvalidArgument, fmt.Sprintf("filesystem limit %s must be zero or between 1 and %d", check.name, check.max))
		}
	}
	return nil
}

func (a adapter) run(ctx context.Context, requested string, execute func(*filesystem.Service) (tool.Result, error)) (tool.Result, error) {
	root, err := a.roots.ResolveRoot(ctx, requested)
	if err != nil {
		return tool.Result{}, tool.WrapError(tool.CodePermissionDenied, "filesystem root was not authorized", err)
	}
	if root == "" {
		return tool.Result{}, tool.NewError(tool.CodePermissionDenied, "filesystem root capability resolved an empty root")
	}
	service, err := filesystem.Open(root, filesystem.WithLimits(a.limits))
	if err != nil {
		return tool.Result{}, mapFilesystemError("open filesystem root", err)
	}
	result, runErr := execute(service)
	closeErr := service.Close()
	if runErr != nil {
		return tool.Result{}, mapFilesystemError("filesystem operation failed", runErr)
	}
	if closeErr != nil {
		return tool.Result{}, mapFilesystemError("close filesystem root", closeErr)
	}
	return result, nil
}
