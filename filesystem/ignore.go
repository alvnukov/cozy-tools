package filesystem

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"
)

type ignoreRule struct {
	negate  bool
	dirOnly bool
	direct  *regexp.Regexp
	re      *regexp.Regexp
}

type ignoreMatcher struct {
	rules []ignoreRule
}

func (s *Service) loadIgnoreMatcher() ignoreMatcher {
	file, err := s.root.Open(".gitignore")
	if err != nil {
		return ignoreMatcher{}
	}
	defer func() { _ = file.Close() }()
	reader := bufio.NewReader(io.LimitReader(file, 1<<20))
	var matcher ignoreMatcher
	for len(matcher.rules) < 10_000 {
		line, readErr := reader.ReadString('\n')
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if rule, ok := parseIgnoreRule(line); ok {
			matcher.rules = append(matcher.rules, rule)
		}
		if readErr != nil {
			break
		}
	}
	return matcher
}

func parseIgnoreRule(line string) (ignoreRule, bool) {
	if line == "" || strings.HasPrefix(line, "#") {
		return ignoreRule{}, false
	}
	negate := false
	if strings.HasPrefix(line, "!") {
		negate = true
		line = strings.TrimPrefix(line, "!")
	} else if strings.HasPrefix(line, "\\!") || strings.HasPrefix(line, "\\#") {
		line = line[1:]
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return ignoreRule{}, false
	}
	dirOnly := strings.HasSuffix(line, "/")
	line = strings.TrimSuffix(line, "/")
	anchored := strings.HasPrefix(line, "/")
	line = strings.TrimPrefix(line, "/")
	hasSlash := strings.Contains(line, "/")
	body, err := globBody(line)
	if err != nil {
		return ignoreRule{}, false
	}
	var expression string
	if anchored || hasSlash {
		expression = "^" + body
	} else {
		expression = "(?:^|/)" + body
	}
	direct, err := regexp.Compile(expression + "$")
	if err != nil {
		return ignoreRule{}, false
	}
	if dirOnly {
		expression += "(?:/.*)?$"
	} else if anchored || hasSlash {
		expression += "(?:$|/.*)"
	} else {
		expression += "(?:$|/)"
	}
	re, err := regexp.Compile(expression)
	if err != nil {
		return ignoreRule{}, false
	}
	return ignoreRule{negate: negate, dirOnly: dirOnly, direct: direct, re: re}, true
}

func (m ignoreMatcher) ignored(name string, isDir bool) bool {
	ignored := false
	for _, rule := range m.rules {
		if !rule.re.MatchString(name) {
			continue
		}
		if rule.dirOnly && !isDir && rule.direct.MatchString(name) {
			continue
		}
		ignored = !rule.negate
	}
	return ignored
}

func compileGlob(pattern string) (*regexp.Regexp, error) {
	if pattern == "" {
		return nil, fmt.Errorf("empty glob")
	}
	body, err := globBody(strings.TrimPrefix(pattern, "./"))
	if err != nil {
		return nil, err
	}
	return regexp.Compile("^" + body + "$")
}

func globBody(pattern string) (string, error) {
	var out strings.Builder
	for i := 0; i < len(pattern); {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i += 2
				if i < len(pattern) && pattern[i] == '/' {
					i++
					out.WriteString("(?:.*/)?")
				} else {
					out.WriteString(".*")
				}
			} else {
				i++
				out.WriteString("[^/]*")
			}
		case '?':
			i++
			out.WriteString("[^/]")
		case '[':
			end := strings.IndexByte(pattern[i+1:], ']')
			if end < 0 {
				return "", fmt.Errorf("unterminated character class")
			}
			end += i + 1
			class := pattern[i+1 : end]
			if strings.HasPrefix(class, "!") {
				class = "^" + regexp.QuoteMeta(class[1:])
			} else {
				class = regexp.QuoteMeta(class)
			}
			out.WriteByte('[')
			out.WriteString(class)
			out.WriteByte(']')
			i = end + 1
		case '\\':
			if i+1 < len(pattern) {
				out.WriteString(regexp.QuoteMeta(string(pattern[i+1])))
				i += 2
			} else {
				out.WriteString("\\\\")
				i++
			}
		default:
			out.WriteString(regexp.QuoteMeta(string(pattern[i])))
			i++
		}
	}
	return out.String(), nil
}
