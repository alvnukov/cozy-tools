package filesystem

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

type openConfig struct {
	limits Limits
}

// Service provides context-aware filesystem operations confined to one root.
// It is safe for concurrent reads; mutating operations are serialized so their
// optimistic guards observe a consistent in-process snapshot.
type Service struct {
	root    *os.Root
	limits  Limits
	mu      sync.RWMutex
	closed  bool
	writeMu sync.Mutex
}

// Open opens rootPath as a confined filesystem root.
func Open(rootPath string, options ...Option) (*Service, error) {
	if rootPath == "" {
		return nil, newError(CodeInvalidInput, "open", rootPath, "root path is required", nil)
	}
	config := openConfig{limits: DefaultLimits()}
	for _, option := range options {
		if option == nil {
			return nil, newError(CodeInvalidInput, "open", rootPath, "nil option", nil)
		}
		if err := option(&config); err != nil {
			return nil, err
		}
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, classifyError("open", rootPath, err)
	}
	return &Service{root: root, limits: config.limits}, nil
}

// Close releases the confined root. It is safe to call more than once.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if err := s.root.Close(); err != nil {
		return classifyError("close", "", err)
	}
	return nil
}

// Limits returns the immutable configured service limits.
func (s *Service) Limits() Limits {
	if s == nil {
		return Limits{}
	}
	return s.limits
}

func (s *Service) begin(ctx context.Context, op string) (func(), error) {
	if s == nil {
		return nil, newError(CodeInternal, op, "", "nil service", nil)
	}
	if ctx == nil {
		return nil, newError(CodeInvalidInput, op, "", "nil context", nil)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil, newError(CodeInternal, op, "", "service is closed", os.ErrClosed)
	}
	return s.mu.RUnlock, nil
}

func cleanPath(name string, allowRoot bool) (string, error) {
	if strings.IndexByte(name, 0) >= 0 {
		return "", newError(CodeInvalidInput, "path", name, "path contains NUL", nil)
	}
	if filepath.IsAbs(name) || filepath.VolumeName(name) != "" || strings.HasPrefix(name, "/") {
		return "", newError(CodeInvalidInput, "path", name, "absolute paths are not allowed", nil)
	}
	for _, part := range strings.Split(strings.ReplaceAll(name, "\\", "/"), "/") {
		if part == ".." {
			return "", newError(CodeInvalidInput, "path", name, "parent-directory paths are not allowed", nil)
		}
	}
	cleaned := path.Clean(strings.ReplaceAll(name, "\\", "/"))
	if cleaned == "." {
		if allowRoot {
			return ".", nil
		}
		return "", newError(CodeInvalidInput, "path", name, "file path is required", nil)
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", newError(CodeInvalidInput, "path", name, "path escapes root", nil)
	}
	return cleaned, nil
}

func mergeLimits(base, requested Limits) (Limits, error) {
	result := base
	var err error
	result.WholeFileBytes, err = lowerInt64("whole_file_bytes", base.WholeFileBytes, requested.WholeFileBytes)
	if err != nil {
		return Limits{}, err
	}
	result.ReadLines, err = lowerInt("read_lines", base.ReadLines, requested.ReadLines)
	if err != nil {
		return Limits{}, err
	}
	result.ReadBytes, err = lowerInt64("read_bytes", base.ReadBytes, requested.ReadBytes)
	if err != nil {
		return Limits{}, err
	}
	result.ListEntries, err = lowerInt("list_entries", base.ListEntries, requested.ListEntries)
	if err != nil {
		return Limits{}, err
	}
	result.FindMatches, err = lowerInt("find_matches", base.FindMatches, requested.FindMatches)
	if err != nil {
		return Limits{}, err
	}
	result.SearchMatches, err = lowerInt("search_matches", base.SearchMatches, requested.SearchMatches)
	if err != nil {
		return Limits{}, err
	}
	result.SearchBytes, err = lowerInt64("search_bytes", base.SearchBytes, requested.SearchBytes)
	if err != nil {
		return Limits{}, err
	}
	result.ScanFiles, err = lowerInt("scan_files", base.ScanFiles, requested.ScanFiles)
	if err != nil {
		return Limits{}, err
	}
	result.ScanBytes, err = lowerInt64("scan_bytes", base.ScanBytes, requested.ScanBytes)
	if err != nil {
		return Limits{}, err
	}
	return result, nil
}

func lowerInt(field string, cap, requested int) (int, error) {
	if requested == 0 {
		return cap, nil
	}
	if requested < 0 || requested > cap {
		return 0, newError(CodeInvalidInput, "limits", field, "limit must be positive and may not exceed configured cap", nil)
	}
	return requested, nil
}

func lowerInt64(field string, cap, requested int64) (int64, error) {
	if requested == 0 {
		return cap, nil
	}
	if requested < 0 || requested > cap {
		return 0, newError(CodeInvalidInput, "limits", field, "limit must be positive and may not exceed configured cap", nil)
	}
	return requested, nil
}

func requestInt(field string, configured, requested int) (int, error) {
	return lowerInt(field, configured, requested)
}

func requestInt64(field string, configured, requested int64) (int64, error) {
	return lowerInt64(field, configured, requested)
}

func isUnsafeRootError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "path escapes") || strings.Contains(text, "escapes from") || strings.Contains(text, "outside root") || strings.Contains(text, "invalid argument")
}

func rootError(op, name string, err error) error {
	if isUnsafeRootError(err) {
		return newError(CodePermission, op, name, "path traversal refused", err)
	}
	return classifyError(op, name, err)
}

func checkContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func errorIsNotExist(err error) bool { return errors.Is(err, os.ErrNotExist) }
