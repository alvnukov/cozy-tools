package filesystem

import (
	"context"
	"math"
	"path"
	"sort"
)

// List recursively lists an addressed path without following symlink directories.
func (s *Service) List(ctx context.Context, request ListRequest) (ListResult, error) {
	var result ListResult
	done, err := s.begin(ctx, "list")
	if err != nil {
		return result, err
	}
	defer done()
	start, err := cleanPath(request.Path, true)
	if err != nil {
		return result, err
	}
	maxDepth := request.MaxDepth
	if maxDepth == 0 {
		maxDepth = 3
	}
	if maxDepth < 0 {
		return result, newError(CodeInvalidInput, "list", start, "max_depth must not be negative", nil)
	}
	maxEntries, err := requestInt("max_entries", s.limits.ListEntries, request.MaxEntries)
	if err != nil {
		return result, err
	}
	scanFiles, err := requestInt("scan_files", s.limits.ScanFiles, request.ScanFiles)
	if err != nil {
		return result, err
	}
	scanBytes, err := requestInt64("scan_bytes", s.limits.ScanBytes, request.ScanBytes)
	if err != nil {
		return result, err
	}
	state, err := s.walk(ctx, walkOptions{
		start: start, maxDepth: maxDepth, showHidden: request.ShowHidden,
		scanFiles: scanFiles, scanBytes: scanBytes,
		explicit: explicitSkipAddress(start, ""), ignore: s.loadIgnoreMatcher(),
	})
	if err != nil {
		return result, err
	}
	entries := make([]Entry, 0, minInt(len(state.items), maxEntries))
	for _, item := range state.items {
		if len(entries) == maxEntries {
			break
		}
		entries = append(entries, itemEntry(item))
	}
	result.Root = start
	result.Entries = entries
	result.Tree = renderTree(start, entries)
	result.Truncation = walkTruncation(state)
	result.Truncation.ReturnedItems = len(entries)
	if len(state.items) > maxEntries {
		result.Truncation.Truncated = true
		result.Truncation.Reason = "list entry limit"
		result.Truncation.Continuation = continuationFor("entry", len(entries))
	}
	return result, nil
}

// Find returns deterministic paths matching a glob, including globstar (**).
func (s *Service) Find(ctx context.Context, request FindRequest) (FindResult, error) {
	var result FindResult
	done, err := s.begin(ctx, "find")
	if err != nil {
		return result, err
	}
	defer done()
	start, err := cleanPath(request.Path, true)
	if err != nil {
		return result, err
	}
	matcher, err := compileGlob(request.Pattern)
	if err != nil {
		return result, newError(CodeInvalidInput, "find", request.Pattern, "invalid glob", err)
	}
	limit, err := requestInt("limit", s.limits.FindMatches, request.Limit)
	if err != nil {
		return result, err
	}
	scanFiles, err := requestInt("scan_files", s.limits.ScanFiles, request.ScanFiles)
	if err != nil {
		return result, err
	}
	scanBytes, err := requestInt64("scan_bytes", s.limits.ScanBytes, request.ScanBytes)
	if err != nil {
		return result, err
	}
	state, err := s.walk(ctx, walkOptions{
		start: start, maxDepth: math.MaxInt, showHidden: request.ShowHidden,
		scanFiles: scanFiles, scanBytes: scanBytes,
		explicit: explicitSkipAddress(start, request.Pattern), ignore: s.loadIgnoreMatcher(),
	})
	if err != nil {
		return result, err
	}
	type match struct {
		path  string
		entry Entry
	}
	matches := make([]match, 0)
	for _, item := range state.items {
		rootRelative := item.name
		relative := item.rel
		if matcher.MatchString(relative) || matcher.MatchString(rootRelative) || matcher.MatchString(path.Base(rootRelative)) {
			matches = append(matches, match{path: rootRelative, entry: itemEntry(item)})
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].path < matches[j].path })
	count := minInt(len(matches), limit)
	result.Paths = make([]string, count)
	result.Entries = make([]Entry, count)
	for i := 0; i < count; i++ {
		result.Paths[i], result.Entries[i] = matches[i].path, matches[i].entry
	}
	result.Truncation = walkTruncation(state)
	result.Truncation.ReturnedItems = count
	if len(matches) > limit {
		result.Truncation.Truncated = true
		result.Truncation.Reason = "find match limit"
		result.Truncation.Continuation = continuationFor("match", count)
	}
	return result, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
