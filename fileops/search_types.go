package fileops

import (
	"fmt"
	"sort"
	"strings"
)

// searchTypeGlobs maps rg-style type names to the globs that define them
// (rg -t/-T). A type include folds into Glob, a type exclude into
// GlobExclude, so types and explicit globs combine without a new mechanism.
var searchTypeGlobs = map[string][]string{
	"c":     {"*.c", "*.h"},
	"cpp":   {"*.cpp", "*.cc", "*.cxx", "*.hpp", "*.hh", "*.h"},
	"cs":    {"*.cs"},
	"css":   {"*.css"},
	"go":    {"*.go"},
	"html":  {"*.html", "*.htm"},
	"java":  {"*.java"},
	"js":    {"*.js", "*.mjs", "*.cjs"},
	"json":  {"*.json"},
	"kt":    {"*.kt", "*.kts"},
	"lua":   {"*.lua"},
	"make":  {"Makefile", "makefile", "GNUmakefile", "*.mk"},
	"md":    {"*.md", "*.markdown"},
	"php":   {"*.php"},
	"py":    {"*.py"},
	"rb":    {"*.rb"},
	"rs":    {"*.rs"},
	"sh":    {"*.sh", "*.bash", "*.zsh"},
	"sql":   {"*.sql"},
	"swift": {"*.swift"},
	"toml":  {"*.toml"},
	"ts":    {"*.ts", "*.tsx"},
	"vim":   {"*.vim"},
	"xml":   {"*.xml"},
	"yaml":  {"*.yaml", "*.yml"},
}

// searchTypeAliases map friendly spellings onto canonical type names.
var searchTypeAliases = map[string]string{
	"golang":     "go",
	"javascript": "js",
	"markdown":   "md",
	"python":     "py",
	"rust":       "rs",
	"typescript": "ts",
}

// typeGlobs resolves type names to globs. An unknown name fails loudly
// because a silently empty filter would read as "no matches anywhere".
func typeGlobs(names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	known := make([]string, 0, len(searchTypeGlobs))
	for name := range searchTypeGlobs {
		known = append(known, name)
	}
	sort.Strings(known)
	var globs []string
	for _, name := range names {
		key := strings.ToLower(strings.TrimSpace(name))
		if alias, ok := searchTypeAliases[key]; ok {
			key = alias
		}
		set, ok := searchTypeGlobs[key]
		if !ok {
			return nil, fmt.Errorf("unknown search type %q (known: %s)", name, strings.Join(known, ", "))
		}
		globs = append(globs, set...)
	}
	return globs, nil
}
