package filesystem

import (
	"context"
	"io"
	"math"
	"regexp"
	"strings"
)

// Search searches regular text files within bounded match and scan budgets.
func (s *Service) Search(ctx context.Context, request SearchRequest) (SearchResult, error) {
	var result SearchResult
	done, err := s.begin(ctx, "search")
	if err != nil {
		return result, err
	}
	defer done()
	start, err := cleanPath(request.Path, true)
	if err != nil {
		return result, err
	}
	if request.Pattern == "" {
		return result, newError(CodeInvalidInput, "search", start, "pattern is required", nil)
	}
	if request.Before < 0 || request.After < 0 {
		return result, newError(CodeInvalidInput, "search", start, "context counts must not be negative", nil)
	}
	limit, err := requestInt("limit", s.limits.SearchMatches, request.Limit)
	if err != nil {
		return result, err
	}
	maxBytes, err := requestInt64("max_bytes", s.limits.SearchBytes, request.MaxBytes)
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

	matchesLine, err := searchPredicate(request)
	if err != nil {
		return result, newError(CodeInvalidInput, "search", request.Pattern, "invalid regular expression", err)
	}
	filters, err := searchGlobs(request)
	if err != nil {
		return result, newError(CodeInvalidInput, "search", request.Glob, "invalid path glob", err)
	}
	state, err := s.walk(ctx, walkOptions{
		start: start, maxDepth: math.MaxInt, showHidden: request.ShowHidden,
		scanFiles: scanFiles, scanBytes: scanBytes,
		explicit: explicitSkipAddress(start, request.Glob+strings.Join(request.Globs, ",")), ignore: s.loadIgnoreMatcher(),
	})
	if err != nil {
		return result, err
	}
	result.Matches = make([]SearchMatch, 0)
	used := int64(0)
	stop := false
	for _, item := range state.items {
		if stop {
			break
		}
		if err := checkContext(ctx); err != nil {
			return result, err
		}
		if !item.info.Mode().IsRegular() || !matchAnyGlob(filters, item.rel, item.name) {
			continue
		}
		if item.info.Size() > s.limits.WholeFileBytes {
			result.SkippedFiles++
			continue
		}
		file, openErr := s.root.Open(item.name)
		if openErr != nil {
			return result, rootError("search", item.name, openErr)
		}
		raw, readErr := io.ReadAll(io.LimitReader(file, s.limits.WholeFileBytes+1))
		closeErr := file.Close()
		if readErr != nil {
			return result, rootError("search", item.name, readErr)
		}
		if closeErr != nil {
			return result, rootError("search", item.name, closeErr)
		}
		if growth := int64(len(raw)) - item.info.Size(); growth > 0 {
			if state.bytes+growth > scanBytes {
				state.truncated = true
				state.reason = "scan byte limit"
				break
			}
			state.bytes += growth
		}
		if int64(len(raw)) > s.limits.WholeFileBytes || isBinary(raw) {
			result.SkippedFiles++
			continue
		}
		lines := splitTextLines(normalizeLF(raw))
		fileHash := hashBytes(raw)
		for index, text := range lines {
			if index%256 == 0 {
				if err := checkContext(ctx); err != nil {
					return result, err
				}
			}
			if !matchesLine(text) {
				continue
			}
			match := makeSearchMatch(item.name, fileHash, lines, index, request.Before, request.After)
			cost := searchMatchBytes(match)
			if len(result.Matches) >= limit {
				result.Truncation.Truncated = true
				result.Truncation.Reason = "search match limit"
				stop = true
				break
			}
			if used+cost > maxBytes {
				result.Truncation.Truncated = true
				result.Truncation.Reason = "search result byte limit"
				stop = true
				break
			}
			result.Matches = append(result.Matches, match)
			used += cost
		}
	}
	if !result.Truncation.Truncated && state.truncated {
		result.Truncation = walkTruncation(state)
	} else {
		result.Truncation.ScannedFiles = state.scanned
		result.Truncation.ScannedBytes = state.bytes
	}
	result.Truncation.ReturnedItems = len(result.Matches)
	result.Truncation.ReturnedBytes = used
	if result.Truncation.Truncated && result.Truncation.Continuation == "" {
		result.Truncation.Continuation = continuationFor("match", len(result.Matches))
	}
	return result, nil
}

func searchPredicate(request SearchRequest) (func(string) bool, error) {
	if request.Literal {
		needle := request.Pattern
		if request.CaseInsensitive {
			needle = strings.ToLower(needle)
		}
		return func(line string) bool {
			if request.CaseInsensitive {
				line = strings.ToLower(line)
			}
			return strings.Contains(line, needle)
		}, nil
	}
	pattern := request.Pattern
	if request.CaseInsensitive {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	return re.MatchString, nil
}

func searchGlobs(request SearchRequest) ([]*regexp.Regexp, error) {
	patterns := append([]string(nil), request.Globs...)
	if request.Glob != "" {
		patterns = append(patterns, request.Glob)
	}
	filters := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		re, err := compileGlob(pattern)
		if err != nil {
			return nil, err
		}
		filters = append(filters, re)
	}
	return filters, nil
}

func matchAnyGlob(filters []*regexp.Regexp, relative, rooted string) bool {
	if len(filters) == 0 {
		return true
	}
	for _, filter := range filters {
		if filter.MatchString(relative) || filter.MatchString(rooted) {
			return true
		}
	}
	return false
}

func makeSearchMatch(name, sha256 string, lines []string, index, before, after int) SearchMatch {
	match := SearchMatch{Path: name, SHA256: sha256, Line: index + 1, Anchor: lineAnchor(lines[index]), Text: lines[index]}
	start := 0
	if before < index {
		start = index - before
	}
	for i := start; i < index; i++ {
		match.Before = append(match.Before, ContextLine{Number: i + 1, Anchor: lineAnchor(lines[i]), Text: lines[i]})
	}
	end := len(lines)
	remaining := len(lines) - index - 1
	if after < remaining {
		end = index + after + 1
	}
	for i := index + 1; i < end; i++ {
		match.After = append(match.After, ContextLine{Number: i + 1, Anchor: lineAnchor(lines[i]), Text: lines[i]})
	}
	return match
}

func searchMatchBytes(match SearchMatch) int64 {
	bytes := int64(len(match.Path) + len(match.Text) + 1)
	for _, line := range match.Before {
		bytes += int64(len(line.Text) + 1)
	}
	for _, line := range match.After {
		bytes += int64(len(line.Text) + 1)
	}
	return bytes
}
