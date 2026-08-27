package filesystem

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
)

type walkOptions struct {
	start      string
	maxDepth   int
	showHidden bool
	scanFiles  int
	scanBytes  int64
	explicit   bool
	ignore     ignoreMatcher
}

type walkItem struct {
	name  string
	rel   string
	depth int
	info  os.FileInfo
}

type walkState struct {
	items     []walkItem
	truncated bool
	reason    string
	scanned   int
	bytes     int64
}

var defaultSkippedDirectories = map[string]struct{}{
	".git": {}, ".hg": {}, ".svn": {}, ".cache": {}, ".mypy_cache": {},
	".pytest_cache": {}, ".ruff_cache": {}, ".venv": {}, "__pycache__": {},
	"build": {}, "dist": {}, "node_modules": {}, "target": {}, "vendor": {}, "venv": {},
}

func (s *Service) walk(ctx context.Context, options walkOptions) (walkState, error) {
	state := walkState{items: make([]walkItem, 0)}
	info, err := s.root.Lstat(options.start)
	if err != nil {
		return state, rootError("walk", options.start, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		size := int64(0)
		if info.Mode().IsRegular() {
			size = max64(info.Size(), 0)
		}
		state.scanned = 1
		if size > options.scanBytes {
			state.truncated = true
			state.reason = "scan byte limit"
			return state, nil
		}
		state.bytes = size
		state.items = append(state.items, walkItem{name: options.start, rel: path.Base(options.start), info: info})
		return state, nil
	}

	var visit func(string, string, int) error
	visit = func(directory, relDirectory string, depth int) error {
		if err := checkContext(ctx); err != nil {
			return err
		}
		file, err := s.root.Open(directory)
		if err != nil {
			return rootError("walk", directory, err)
		}
		remaining := options.scanFiles - state.scanned
		if remaining < 1 {
			_ = file.Close()
			state.truncated = true
			state.reason = "scan file limit"
			return nil
		}
		entries, readErr := file.ReadDir(remaining + 1)
		closeErr := file.Close()
		if readErr != nil && readErr != io.EOF {
			return rootError("walk", directory, readErr)
		}
		if closeErr != nil {
			return rootError("walk", directory, closeErr)
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		overflow := len(entries) > remaining
		if overflow {
			entries = entries[:remaining]
		}
		for _, entry := range entries {
			if state.scanned >= options.scanFiles {
				state.truncated = true
				state.reason = "scan file limit"
				break
			}
			if err := checkContext(ctx); err != nil {
				return err
			}
			base := entry.Name()
			child := path.Join(directory, base)
			rel := base
			if relDirectory != "" {
				rel = path.Join(relDirectory, base)
			}
			entryInfo, infoErr := entry.Info()
			if infoErr != nil {
				return rootError("walk", child, infoErr)
			}
			state.scanned++
			size := int64(0)
			if entryInfo.Mode().IsRegular() {
				size = max64(entryInfo.Size(), 0)
			}
			if state.bytes+size > options.scanBytes {
				state.truncated = true
				state.reason = "scan byte limit"
				return nil
			}
			state.bytes += size
			if !options.showHidden && strings.HasPrefix(base, ".") {
				continue
			}
			if entryInfo.IsDir() && !options.explicit {
				if _, skip := defaultSkippedDirectories[base]; skip {
					continue
				}
			}
			ignored := options.ignore.ignored(child, entryInfo.IsDir())
			if !ignored {
				state.items = append(state.items, walkItem{name: child, rel: rel, depth: depth + 1, info: entryInfo})
			}
			if entryInfo.IsDir() && entryInfo.Mode()&os.ModeSymlink == 0 && depth+1 < options.maxDepth {
				if err := visit(child, rel, depth+1); err != nil {
					return err
				}
				if state.truncated {
					return nil
				}
			}
		}
		if overflow && !state.truncated {
			state.truncated = true
			state.reason = "scan file limit"
		}
		return nil
	}
	if options.maxDepth > 0 {
		if err := visit(options.start, "", 0); err != nil {
			return state, err
		}
	}
	return state, nil
}

func itemEntry(item walkItem) Entry {
	typeName := "file"
	switch {
	case item.info.Mode()&os.ModeSymlink != 0:
		typeName = "symlink"
	case item.info.IsDir():
		typeName = "directory"
	case !item.info.Mode().IsRegular():
		typeName = "other"
	}
	return Entry{Path: item.name, Name: path.Base(item.name), Type: typeName, Depth: item.depth, Size: item.info.Size(), Mode: item.info.Mode()}
}

func explicitSkipAddress(start, pattern string) bool {
	combined := start + "/" + pattern
	for name := range defaultSkippedDirectories {
		for _, component := range strings.Split(strings.Trim(combined, "/"), "/") {
			if component == name {
				return true
			}
		}
		if strings.Contains(pattern, name) {
			return true
		}
	}
	return false
}

func walkTruncation(state walkState) Truncation {
	return Truncation{Truncated: state.truncated, Reason: state.reason, ScannedFiles: state.scanned, ScannedBytes: state.bytes}
}

func renderTree(root string, entries []Entry) string {
	var builder strings.Builder
	builder.WriteString(root)
	builder.WriteByte('\n')
	for _, entry := range entries {
		depth := entry.Depth
		if depth < 1 {
			depth = 1
		}
		builder.WriteString(strings.Repeat("|   ", depth-1))
		builder.WriteString("|-- ")
		builder.WriteString(entry.Name)
		if entry.Type == "directory" {
			builder.WriteByte('/')
		}
		builder.WriteByte('\n')
	}
	return strings.TrimSuffix(builder.String(), "\n")
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func continuationFor(kind string, count int) string { return fmt.Sprintf("%s_after=%d", kind, count) }
