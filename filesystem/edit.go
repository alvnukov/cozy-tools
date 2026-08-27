package filesystem

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var hashlinePattern = regexp.MustCompile(`^([1-9][0-9]*)#([a-z]{3})$`)

type resolvedHashlineEdit struct {
	start   int
	end     int
	content []string
}

// Edit applies one guarded unique replacement or a set of hashline ranges to
// one text snapshot, then atomically installs the result.
func (s *Service) Edit(ctx context.Context, request EditRequest) (EditResult, error) {
	var result EditResult
	done, err := s.begin(ctx, "edit")
	if err != nil {
		return result, err
	}
	defer done()
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	name, err := cleanPath(request.Path, false)
	if err != nil {
		return result, err
	}
	if err := validateExpectedHash(request.ExpectedSHA256); err != nil {
		return result, err
	}
	hasHashline := len(request.Edits) > 0
	hasReplace := request.Replace != nil || request.OldText != ""
	if hasHashline == hasReplace {
		return result, newError(CodeInvalidInput, "edit", name, "select exactly one replacement style", nil)
	}
	if hasHashline && request.ExpectedSHA256 == "" {
		return result, newError(CodeInvalidInput, "edit", name, "hashline edits require expected_sha256", nil)
	}
	raw, info, exists, err := s.readExisting(ctx, name)
	if err != nil {
		return result, err
	}
	if !exists {
		return result, newError(CodeNotFound, "edit", name, "path not found", os.ErrNotExist)
	}
	if isBinary(raw) {
		return result, newError(CodeInvalidInput, "edit", name, "binary files cannot be text-edited", nil)
	}
	oldHash := hashBytes(raw)
	if err := matchExpected(name, oldHash, request.ExpectedSHA256); err != nil {
		return result, err
	}

	newline := newlineConvention(raw)
	finalNewline := bytes.HasSuffix(raw, []byte("\n"))
	normalized := string(normalizeLF(raw))
	if finalNewline {
		normalized = strings.TrimSuffix(normalized, "\n")
	}
	if hasHashline && len(raw) == 0 {
		return result, newError(CodeConflict, "edit", name, "empty snapshot has no hashline anchors", nil)
	}
	var edited string
	if hasHashline {
		edited, err = applyHashlineEdits(normalized, request.Edits)
	} else {
		replacement := request.Replace
		if replacement == nil {
			replacement = &ReplaceEdit{Old: request.OldText, New: request.NewText}
		}
		edited, err = applyUniqueReplacement(normalized, replacement.Old, replacement.New)
	}
	if err != nil {
		return result, err
	}
	if finalNewline {
		edited += "\n"
	} else {
		edited = strings.TrimRight(edited, "\n")
	}
	newRaw := []byte(edited)
	if newline == "\r\n" {
		newRaw = bytes.ReplaceAll(newRaw, []byte("\n"), []byte("\r\n"))
	}
	if int64(len(newRaw)) > s.limits.WholeFileBytes {
		return result, newError(CodeLimit, "edit", name, "edited file exceeds whole-file cap", nil)
	}
	newHash := hashBytes(newRaw)
	if newHash == oldHash {
		return EditResult{Path: name, OldSHA256: oldHash, NewSHA256: oldHash, Changed: false, Status: "unchanged"}, nil
	}
	if err := checkContext(ctx); err != nil {
		return result, err
	}
	if err := s.atomicWrite(ctx, name, newRaw, preservedMode(info.Mode()), true, false); err != nil {
		return result, err
	}
	return EditResult{Path: name, OldSHA256: oldHash, NewSHA256: newHash, Changed: true, Status: "updated"}, nil
}

func applyUniqueReplacement(snapshot, oldText, newText string) (string, error) {
	oldText = string(normalizeLF([]byte(oldText)))
	newText = string(normalizeLF([]byte(newText)))
	if oldText == "" {
		if snapshot == "" {
			return newText, nil
		}
		return "", newError(CodeInvalidInput, "edit", "", "old text must not be empty unless replacing an empty snapshot", nil)
	}
	count := strings.Count(snapshot, oldText)
	switch {
	case count == 1:
		return strings.Replace(snapshot, oldText, newText, 1), nil
	case count == 0 && strings.Contains(snapshot, newText):
		return snapshot, nil
	case count == 0:
		return "", newError(CodeConflict, "edit", "", "old text was not found", nil)
	default:
		return "", newError(CodeConflict, "edit", "", fmt.Sprintf("old text occurs %d times", count), nil)
	}
}

func applyHashlineEdits(snapshot string, edits []HashlineEdit) (string, error) {
	lines := strings.Split(snapshot, "\n")
	resolved := make([]resolvedHashlineEdit, 0, len(edits))
	for _, edit := range edits {
		fromLine, fromAnchor, err := parseHashline(edit.From)
		if err != nil {
			return "", err
		}
		toText := edit.To
		if toText == "" {
			toText = edit.From
		}
		toLine, toAnchor, err := parseHashline(toText)
		if err != nil {
			return "", err
		}
		if fromLine > toLine {
			return "", newError(CodeInvalidInput, "edit", edit.From+".."+toText, "range is reversed", nil)
		}
		if fromLine < 1 || toLine > len(lines) {
			return "", newError(CodeConflict, "edit", edit.From+".."+toText, "line range is outside snapshot", nil)
		}
		if lineAnchor(lines[fromLine-1]) != fromAnchor || lineAnchor(lines[toLine-1]) != toAnchor {
			return "", newError(CodeConflict, "edit", edit.From+".."+toText, "hashline anchor mismatch", nil)
		}
		var content []string
		if edit.Content != nil {
			normalized := string(normalizeLF([]byte(*edit.Content)))
			if normalized == "" {
				content = []string{""}
			} else {
				content = splitTextLines([]byte(normalized))
			}
		}
		resolved = append(resolved, resolvedHashlineEdit{start: fromLine - 1, end: toLine, content: content})
	}
	sort.Slice(resolved, func(i, j int) bool { return resolved[i].start < resolved[j].start })
	for i := 1; i < len(resolved); i++ {
		if resolved[i].start < resolved[i-1].end {
			return "", newError(CodeConflict, "edit", "", "hashline ranges overlap", nil)
		}
	}
	for i := len(resolved) - 1; i >= 0; i-- {
		edit := resolved[i]
		next := make([]string, 0, len(lines)-(edit.end-edit.start)+len(edit.content))
		next = append(next, lines[:edit.start]...)
		next = append(next, edit.content...)
		next = append(next, lines[edit.end:]...)
		lines = next
	}
	return strings.Join(lines, "\n"), nil
}

func parseHashline(value string) (int, string, error) {
	match := hashlinePattern.FindStringSubmatch(value)
	if match == nil {
		return 0, "", newError(CodeInvalidInput, "edit", value, "hashline must be number#abc", nil)
	}
	line, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, "", newError(CodeInvalidInput, "edit", value, "invalid line number", err)
	}
	return line, match[2], nil
}

func newlineConvention(raw []byte) string {
	crlf := bytes.Count(raw, []byte("\r\n"))
	lf := bytes.Count(raw, []byte("\n")) - crlf
	if crlf > 0 && crlf >= lf {
		return "\r\n"
	}
	return "\n"
}
