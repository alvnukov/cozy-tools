package fileops

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/alvnukov/cozy-tools/safefs"
)

// SearchResult holds structured search results.
type SearchResult struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
	// Matches are grep's own file:line:text, one string per match, rather than
	// objects with file, line_number and text keys. Those three key names would
	// repeat on every match, which across a full result is most of the payload,
	// and the reader is a model that has met this exact layout in grep and
	// ripgrep output far more often than in any JSON shape of it.
	Matches []string `json:"matches"`
	Total   int      `json:"total"`
	// Truncated reports that the walk stopped at the match cap, so the tree may
	// hold matches this result does not show. Without it Total reads as a count
	// of everything, and a reader draws a conclusion from a partial answer
	// without knowing that it is partial.
	Truncated bool `json:"truncated"`
}

// SearchOptions extends the literal substring search with rg-like modes. The
// zero value keeps the historical behaviour: a literal, case-sensitive
// substring match.
type SearchOptions struct {
	// Pattern is the search text: a literal substring unless Regex is set.
	Pattern string
	// Regex interprets Pattern as a regular expression.
	Regex bool
	// IgnoreCase matches case-insensitively whatever the pattern's case.
	IgnoreCase bool
	// SmartCase matches case-insensitively only while Pattern carries no
	// uppercase letter (rg -S). IgnoreCase wins when both are set.
	SmartCase bool
	// Glob restricts the search to files matching at least one pattern
	// (rg -g). A pattern without a separator matches the base name; one with
	// a separator matches the repo-relative path.
	Glob []string
	// GlobExclude drops files matching any pattern, same layout as Glob
	// (rg -g '!...').
	GlobExclude []string
	// NoIgnore disables the .gitignore/.ignore/.rgignore cascade (rg
	// --no-ignore). The built-in skips — dot directories, vendor trees,
	// binary extensions, the task registry — always apply.
	NoIgnore bool
	// Type and TypeNot include and exclude file types by name (rg -t/-T),
	// e.g. "go", "py", "md". They fold into Glob and GlobExclude; an
	// unknown name is an error, not an empty filter.
	Type    []string
	TypeNot []string
	// WordMatch requires matches to sit between word boundaries (rg -w);
	// LineRegexp requires the pattern to span the whole line (rg -x).
	WordMatch  bool
	LineRegexp bool
	// ContextBefore and ContextAfter add non-matching neighbour lines around
	// each match, marked with '-' instead of ':' as grep does. They do not
	// count towards Total.
	ContextBefore int
	ContextAfter  int
	// FilesOnly reports the paths of files holding at least one match instead
	// of the matching lines (rg -l); Total then counts files.
	FilesOnly bool
	// CountOnly reports "path:count" per file holding matches (rg -c); Total
	// then counts files, not lines.
	CountOnly bool
	// Invert matches the lines the pattern does not hit (rg -v).
	Invert bool
	// OnlyMatching reports each match's own text instead of its whole line
	// (rg -o). Replace rewrites that text through the regex's capture
	// groups (rg -r '$1'); without OnlyMatching it rewrites the matched
	// spans inside the reported lines. Both force the regex matcher.
	OnlyMatching bool
	Replace      string
	// MaxMatches caps total reported matches; 0 means 100.
	MaxMatches int
}

// validateSearchOptions rejects combinations whose meaning would surprise:
// inverted lines have no matched spans to extract, and two per-file summary
// modes cannot both shape the output.
func validateSearchOptions(opts SearchOptions) error {
	if opts.OnlyMatching && opts.Invert {
		return fmt.Errorf("only_matching cannot be combined with invert")
	}
	if opts.CountOnly && opts.FilesOnly {
		return fmt.Errorf("count_only cannot be combined with files_only")
	}
	return nil
}

// searchMatcher decides whether one line matches the configured pattern.
type searchMatcher struct {
	regex   *regexp.Regexp
	literal string
	fold    bool
}

func newSearchMatcher(opts SearchOptions) (searchMatcher, error) {
	icase := opts.IgnoreCase
	if !icase && opts.SmartCase && opts.Pattern == strings.ToLower(opts.Pattern) {
		icase = true
	}
	// Modes that need match positions or boundaries go through the regex
	// engine even for a literal pattern; the plain substring fast path stays
	// for everything else.
	forceRegex := opts.Regex || opts.WordMatch || opts.LineRegexp || opts.OnlyMatching || opts.Replace != ""
	if !forceRegex {
		literal := opts.Pattern
		if icase {
			literal = strings.ToLower(literal)
		}
		return searchMatcher{literal: literal, fold: icase}, nil
	}
	expr := opts.Pattern
	if !opts.Regex {
		expr = regexp.QuoteMeta(expr)
	}
	if opts.WordMatch {
		expr = `\b(?:` + expr + `)\b`
	}
	if opts.LineRegexp {
		expr = `^(?:` + expr + `)$`
	}
	if icase {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return searchMatcher{}, fmt.Errorf("invalid regex pattern %q: %w", opts.Pattern, err)
	}
	return searchMatcher{regex: re}, nil
}

func (m searchMatcher) matches(line string) bool {
	if m.regex != nil {
		return m.regex.MatchString(line)
	}
	if m.fold {
		return strings.Contains(strings.ToLower(line), m.literal)
	}
	return strings.Contains(line, m.literal)
}

// onlyMatches returns the matched spans of one line, or the line itself for
// a literal matcher (only_matching forces the regex path, so the literal
// case is a defensive fallback).
func (m searchMatcher) onlyMatches(line string, replace string) []string {
	if m.regex == nil {
		return []string{line}
	}
	locs := m.regex.FindAllStringSubmatchIndex(line, -1)
	if locs == nil {
		return nil
	}
	out := make([]string, 0, len(locs))
	for _, loc := range locs {
		if replace != "" {
			out = append(out, string(m.regex.ExpandString(nil, replace, line, loc)))
		} else {
			out = append(out, line[loc[0]:loc[1]])
		}
	}
	return out
}

// globSet is a precompiled include/exclude glob list.
type globSet struct {
	regexes []*regexp.Regexp
	byBase  []bool
}

// translateGlob compiles one glob into an anchored regex body: * and ? stay
// within a path segment, ** crosses separators (and a trailing **/ may match
// nothing, so **/*.go finds root files too), [class] passes through, and \
// escapes the next character — the layout rg -g and gitignore share.
func translateGlob(pattern string) string {
	var b strings.Builder
	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				if i+2 < len(pattern) && pattern[i+2] == '/' {
					b.WriteString("(?:.*/)?")
					i += 2
				} else {
					b.WriteString(".*")
					i++
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '[':
			end := strings.IndexByte(pattern[i+1:], ']')
			if end < 0 {
				b.WriteString(`\[`)
				break
			}
			class := pattern[i+1 : i+1+end]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteByte('[')
			b.WriteString(class)
			b.WriteByte(']')
			i += end + 1
		case '\\':
			if i+1 < len(pattern) {
				i++
				b.WriteString(regexp.QuoteMeta(string(pattern[i])))
			} else {
				b.WriteString(regexp.QuoteMeta(`\`))
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return b.String()
}

func compileGlobSet(patterns []string) globSet {
	set := globSet{}
	for _, pattern := range patterns {
		re, err := regexp.Compile("^" + translateGlob(pattern) + "$")
		if err != nil {
			// Unreachable — every fragment is quoted or a literal form.
			continue
		}
		set.regexes = append(set.regexes, re)
		set.byBase = append(set.byBase, !strings.Contains(pattern, "/"))
	}
	return set
}

func (g globSet) empty() bool { return len(g.regexes) == 0 }

func (g globSet) matches(relative string) bool {
	for i, re := range g.regexes {
		candidate := relative
		if g.byBase[i] {
			candidate = path.Base(relative)
		}
		if re.MatchString(candidate) {
			return true
		}
	}
	return false
}

// SearchFiles runs a simple text search in a directory and returns structured results.
// It reads each non-binary file under root, splits into lines, and matches pattern.
func SearchFiles(rootPath string, pattern string, maxMatches int) (SearchResult, error) {
	return SearchFilesWithOptions(rootPath, SearchOptions{Pattern: pattern, MaxMatches: maxMatches})
}

// SearchFilesWithOptions is SearchFiles with the full rg-like option set.
func SearchFilesWithOptions(rootPath string, opts SearchOptions) (SearchResult, error) {
	root, err := safefs.Open(rootPath)
	if err != nil {
		return SearchResult{Pattern: opts.Pattern, Path: rootPath}, err
	}
	defer func() { _ = root.Close() }()
	return searchFilesAtRoot(rootPath, root, ".", opts)
}

// SearchFilesInRepo runs a text search under a repo-relative directory.
func SearchFilesInRepo(repoPath string, filePath string, pattern string, maxMatches int) (SearchResult, error) {
	return SearchFilesInRepoWithOptions(repoPath, filePath, SearchOptions{Pattern: pattern, MaxMatches: maxMatches})
}

// SearchFilesInRepoWithOptions is SearchFilesInRepo with the full rg-like option set.
func SearchFilesInRepoWithOptions(repoPath string, filePath string, opts SearchOptions) (SearchResult, error) {
	root, err := safefs.Open(repoPath)
	if err != nil {
		return SearchResult{}, err
	}
	defer func() { _ = root.Close() }()

	if strings.TrimSpace(filePath) == "" {
		return searchFilesAtRoot(root.Path(), root, ".", opts)
	}
	display, relative, err := repoRelativePath(repoPath, filePath)
	if err != nil {
		return SearchResult{}, err
	}
	return searchFilesAtRoot(display, root, relative, opts)
}

func searchFilesAtRoot(displayPath string, root *safefs.Root, walkRoot string, opts SearchOptions) (SearchResult, error) {
	if opts.MaxMatches <= 0 {
		opts.MaxMatches = 100
	}
	if err := validateSearchOptions(opts); err != nil {
		return SearchResult{}, err
	}
	matcher, err := newSearchMatcher(opts)
	if err != nil {
		return SearchResult{}, err
	}
	include := opts.Glob
	typeInclude, err := typeGlobs(opts.Type)
	if err != nil {
		return SearchResult{}, err
	}
	include = append(include, typeInclude...)
	exclude := opts.GlobExclude
	typeExclude, err := typeGlobs(opts.TypeNot)
	if err != nil {
		return SearchResult{}, err
	}
	exclude = append(exclude, typeExclude...)
	includeGlobs := compileGlobSet(include)
	excludeGlobs := compileGlobSet(exclude)
	walkRoot = filepath.ToSlash(filepath.Clean(walkRoot))
	// A missing walk root must fail loudly with the nearest directory that
	// does exist: a silent zero-match answer reads as "nothing matches
	// anywhere" and sends the caller hunting for a different root cause.
	if _, statErr := root.Stat(filepath.FromSlash(walkRoot)); statErr != nil {
		nearest := nearestExistingDir(root, walkRoot)
		return SearchResult{Pattern: opts.Pattern, Path: displayPath}, fmt.Errorf(
			"search path %q does not exist under repo root %q; nearest existing directory %q — call file action=list on it to see available names",
			walkRoot, root.Path(), nearest)
	}
	var ignores ignoreStack
	if !opts.NoIgnore && walkRoot != "." {
		ignores = ancestorIgnoreLevels(root, walkRoot)
	}
	result := SearchResult{Pattern: opts.Pattern, Path: displayPath}
	err = fs.WalkDir(root.FS(), walkRoot, func(entryPath string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		relative := strings.TrimPrefix(entryPath, walkRoot+"/")
		if entryPath == walkRoot {
			relative = "."
		}
		if !opts.NoIgnore {
			ignores.popLeftDirs(entryPath)
		}
		if d.IsDir() {
			base := path.Base(entryPath)
			if strings.HasPrefix(base, ".") && entryPath != walkRoot {
				return fs.SkipDir
			}
			if base == "node_modules" || base == "__pycache__" || base == "vendor" || isTaskRegistryRelative(relative) {
				return fs.SkipDir
			}
			if !opts.NoIgnore && ignores.ignores(entryPath, true) {
				// An ignored directory is pruned whole: git allows no
				// re-include beneath an excluded parent.
				return fs.SkipDir
			}
			if !opts.NoIgnore {
				ignores = append(ignores, loadDirIgnore(root, entryPath))
			}
			return nil
		}
		// A search naming one file searches it whatever the cascade says,
		// the way `rg pattern file` does.
		if !opts.NoIgnore && entryPath != walkRoot && ignores.ignores(entryPath, false) {
			return nil
		}
		ext := strings.ToLower(path.Ext(entryPath))
		switch ext {
		case ".exe", ".dll", ".so", ".dylib", ".bin", ".jpg", ".png", ".gif", ".ico",
			".zip", ".tar", ".gz", ".bz2", ".xz", ".7z", ".pdf", ".class", ".pyc", ".pyo":
			return nil
		}
		if isProtectedLeanPath(entryPath) {
			return nil
		}
		if relative == "." {
			// The walk names its root "."; for a search scoped to a single
			// file that root is the target, not something to skip.
			relative = path.Base(entryPath)
		}
		if !includeGlobs.empty() && !includeGlobs.matches(relative) {
			return nil
		}
		if !excludeGlobs.empty() && excludeGlobs.matches(relative) {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return nil
		}
		file, openErr := root.OpenFile(filepath.FromSlash(entryPath), 0, 0)
		if openErr != nil {
			return nil
		}
		data, readErr := io.ReadAll(io.LimitReader(file, (1<<20)+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || len(data) > 1<<20 {
			return nil
		}
		// Binary by content, not by name: one NUL byte means the file is not
		// text worth searching, whatever the extension says. rg sniffs the
		// same way.
		if bytes.IndexByte(data, 0) >= 0 {
			return nil
		}
		lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		// A trailing newline splits into one phantom empty line that grep
		// never numbers; it must not become an invert hit or an empty-match
		// regex hit.
		if n := len(lines); n > 0 && lines[n-1] == "" {
			lines = lines[:n-1]
		}
		hitLines := make([]int, 0, 4)
		for i, line := range lines {
			if matcher.matches(line) == opts.Invert {
				continue
			}
			hitLines = append(hitLines, i)
		}
		if len(hitLines) == 0 {
			return nil
		}
		if opts.FilesOnly {
			result.Matches = append(result.Matches, relative)
			result.Total++
			if result.Total >= opts.MaxMatches {
				result.Truncated = true
				return fs.SkipAll
			}
			return nil
		}
		if opts.CountOnly {
			result.Matches = append(result.Matches, fmt.Sprintf("%s:%d", relative, len(hitLines)))
			result.Total++
			if result.Total >= opts.MaxMatches {
				result.Truncated = true
				return fs.SkipAll
			}
			return nil
		}
		if opts.OnlyMatching {
			for _, hit := range hitLines {
				for _, text := range matcher.onlyMatches(lines[hit], opts.Replace) {
					result.Matches = append(result.Matches, fmt.Sprintf("%s:%d:%s", relative, hit+1, text))
					result.Total++
					if result.Total >= opts.MaxMatches {
						result.Truncated = true
						return fs.SkipAll
					}
				}
			}
			return nil
		}
		// Context windows of consecutive hits merge into one run, as in grep,
		// and a matching line keeps its ':' marker even when an earlier
		// window already emitted it.
		hitSet := make(map[int]bool, len(hitLines))
		for _, hit := range hitLines {
			hitSet[hit] = true
		}
		emitted := -1
		for _, hit := range hitLines {
			start := hit - opts.ContextBefore
			if start < 0 {
				start = 0
			}
			if start <= emitted {
				start = emitted + 1
			}
			end := hit + opts.ContextAfter
			if end >= len(lines) {
				end = len(lines) - 1
			}
			for i := start; i <= end; i++ {
				sep := "-"
				text := lines[i]
				if hitSet[i] {
					sep = ":"
					if opts.Replace != "" && matcher.regex != nil {
						text = matcher.regex.ReplaceAllString(lines[i], opts.Replace)
					}
				}
				result.Matches = append(result.Matches, fmt.Sprintf("%s%s%d:%s", relative, sep, i+1, text))
				emitted = i
			}
			result.Total++
			if result.Total >= opts.MaxMatches {
				result.Truncated = true
				return fs.SkipAll
			}
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	return result, nil
}
