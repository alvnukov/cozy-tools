package fileops

import (
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/alvnukov/cozy-tools/safefs"
)

// ignoreFileNames lists the per-directory ignore files ripgrep stacks, in
// ascending precedence: .rgignore patterns override .ignore, which overrides
// .gitignore. Within one directory the concatenated rule list is evaluated
// back to front — the last matching pattern wins, the tie-break git uses
// inside a single file — so the later files keep their priority.
var ignoreFileNames = []string{".gitignore", ".ignore", ".rgignore"}

// ignorePattern is one compiled gitignore rule.
type ignorePattern struct {
	re      *regexp.Regexp
	negate  bool
	dirOnly bool
	byBase  bool
}

// parseIgnoreRules compiles gitignore syntax into patterns. Comments, blank
// lines and unparsable lines are skipped, as git does. A leading ! negates a
// rule, a trailing / restricts it to directories, a / anywhere else anchors
// it to the rule file's directory, and a slash-free rule matches a base name
// at any depth.
func parseIgnoreRules(content string) []ignorePattern {
	var patterns []ignorePattern
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimRight(raw, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = trimUnescapedTrailingSpace(line)
		negate := strings.HasPrefix(line, "!")
		if negate {
			line = line[1:]
		} else if strings.HasPrefix(line, `\!`) || strings.HasPrefix(line, `\#`) {
			line = line[1:]
		}
		dirOnly := strings.HasSuffix(line, "/")
		line = strings.TrimSuffix(line, "/")
		anchored := strings.Contains(line, "/")
		line = strings.TrimPrefix(line, "/")
		if line == "" {
			continue
		}
		re, err := regexp.Compile("^" + translateGlob(line) + "$")
		if err != nil {
			continue
		}
		patterns = append(patterns, ignorePattern{re: re, negate: negate, dirOnly: dirOnly, byBase: !anchored})
	}
	return patterns
}

// trimUnescapedTrailingSpace drops trailing blanks git would drop, keeping a
// blank that a backslash escaped.
func trimUnescapedTrailingSpace(line string) string {
	for len(line) > 0 {
		c := line[len(line)-1]
		if c != ' ' && c != '\t' {
			break
		}
		if len(line) >= 2 && line[len(line)-2] == '\\' {
			break
		}
		line = line[:len(line)-1]
	}
	return line
}

// dirIgnore is one directory's concatenated ignore rules; path is the
// directory's walk path, "." for the repository root.
type dirIgnore struct {
	path  string
	rules []ignorePattern
}

// ignoreStack is the chain of rule levels from the walk root down to the
// directory being walked. Deeper levels decide first; a level only sees
// paths below its own directory.
type ignoreStack []dirIgnore

// ignores reports whether entryPath is dropped by the cascade. A negated
// match re-includes the path, the way git resolves a trailing !rule.
func (s ignoreStack) ignores(entryPath string, isDir bool) bool {
	for i := len(s) - 1; i >= 0; i-- {
		level := s[i]
		rel := entryPath
		if level.path != "." {
			rel = strings.TrimPrefix(entryPath, level.path+"/")
			if rel == entryPath {
				continue
			}
		}
		for j := len(level.rules) - 1; j >= 0; j-- {
			p := level.rules[j]
			if p.dirOnly && !isDir {
				continue
			}
			candidate := rel
			if p.byBase {
				candidate = path.Base(rel)
			}
			if p.re.MatchString(candidate) {
				return !p.negate
			}
		}
	}
	return false
}

// popLeftDirs unwinds levels whose directory stopped being an ancestor of
// entryPath. WalkDir emits no leave events, so the stack unwinds by prefix.
func (s *ignoreStack) popLeftDirs(entryPath string) {
	for len(*s) > 0 {
		top := (*s)[len(*s)-1].path
		if top == "." || strings.HasPrefix(entryPath+"/", top+"/") {
			return
		}
		*s = (*s)[:len(*s)-1]
	}
}

// loadDirIgnore reads one directory's ignore files; a missing file adds
// nothing.
func loadDirIgnore(root *safefs.Root, dirPath string) dirIgnore {
	var rules []ignorePattern
	for _, name := range ignoreFileNames {
		data, err := root.ReadFile(filepath.FromSlash(path.Join(dirPath, name)))
		if err != nil {
			continue
		}
		rules = append(rules, parseIgnoreRules(string(data))...)
	}
	return dirIgnore{path: dirPath, rules: rules}
}

// ancestorIgnoreLevels loads the levels for "." and every proper ancestor of
// walkRoot, so a walk scoped below the root still honours the root's ignore
// files. walkRoot's own level is loaded when the walk visits it.
func ancestorIgnoreLevels(root *safefs.Root, walkRoot string) ignoreStack {
	levels := ignoreStack{loadDirIgnore(root, ".")}
	if walkRoot == "." {
		return levels
	}
	parts := strings.Split(walkRoot, "/")
	acc := ""
	for i := 0; i < len(parts)-1; i++ {
		if acc == "" {
			acc = parts[i]
		} else {
			acc = acc + "/" + parts[i]
		}
		levels = append(levels, loadDirIgnore(root, acc))
	}
	return levels
}
