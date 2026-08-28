// Package fileops provides guarded, idempotent file edit operations.
package fileops

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/alvnukov/cozy-tools/safefs"
)

// Snapshot records file identity before a guarded edit.
type Snapshot struct {
	Path         string   `json:"path"`
	RepoPath     string   `json:"repo_path,omitempty"`
	RelativePath string   `json:"relative_path,omitempty"`
	Hash         string   `json:"hash"`
	HashChunks   []string `json:"hash_chunks,omitempty"`
	Size         int      `json:"size"`
	Exists       bool     `json:"exists"`
}

type scopedFile struct {
	root     *safefs.Root
	name     string
	display  string
	relative string
}

func openScopedFile(repoPath string, filePath string, createParents bool) (*scopedFile, error) {
	if strings.TrimSpace(repoPath) != "" {
		display, relative, err := repoRelativePath(repoPath, filePath)
		if err != nil {
			return nil, err
		}
		root, err := safefs.Open(repoPath)
		if err != nil {
			return nil, err
		}
		if createParents {
			parent := filepath.Dir(filepath.FromSlash(relative))
			if parent != "." {
				if err := root.MkdirAll(parent, 0o700); err != nil {
					_ = root.Close()
					return nil, fmt.Errorf("create parent directories for %q: %w", relative, err)
				}
			}
		}
		return &scopedFile{
			root:     root,
			name:     filepath.FromSlash(relative),
			display:  display,
			relative: relative,
		}, nil
	}

	clean, err := cleanPath(filePath)
	if err != nil {
		return nil, err
	}
	parent := filepath.Dir(clean)
	var root *safefs.Root
	if createParents {
		root, err = safefs.Ensure(parent, 0o700)
	} else {
		root, err = safefs.Open(parent)
	}
	if err != nil {
		return nil, err
	}
	return &scopedFile{
		root:    root,
		name:    filepath.Base(clean),
		display: clean,
	}, nil
}

func (f *scopedFile) close() {
	_ = f.root.Close()
}

// ReplaceRequest describes one unique text replacement guarded by file hash.
// When Old/New contain characters that are difficult to represent in JSON strings
// (e.g. raw Go literals with backslashes), use OldB64 and NewB64 instead —
// they carry the same content base64-encoded and avoid escaping problems.
// ExpectedHashChunks carries the same digest as eight hex chunks — the shape
// snapshot reports as hash_chunks — because long single strings lose
// characters inside tool-call transport.
type ReplaceRequest struct {
	RepoPath           string   `json:"repo_path"`
	Path               string   `json:"path"`
	ExpectedHash       string   `json:"expected_hash"`
	ExpectedHashChunks []string `json:"expected_hash_chunks,omitempty"`
	Old                string   `json:"old"`
	New                string   `json:"new"`
	OldB64             string   `json:"old_b64,omitempty"`
	NewB64             string   `json:"new_b64,omitempty"`
}

// ReplaceResult reports the result of a guarded replacement.
type ReplaceResult struct {
	Status  string `json:"status"`
	Path    string `json:"path"`
	Changed bool   `json:"changed"`
	OldHash string `json:"old_hash"`
	NewHash string `json:"new_hash"`
	Reason  string `json:"reason,omitempty"`
}

// ReadSnapshot returns file hash and size for an absolute or process-relative path.
func ReadSnapshot(path string) (Snapshot, error) {
	clean, err := cleanPath(path)
	if err != nil {
		return Snapshot{}, err
	}
	// #nosec G304 -- cleanPath resolves the caller-specified local path before reading.
	data, err := os.ReadFile(clean)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Snapshot{Path: clean, Exists: false}, nil
		}
		return Snapshot{}, err
	}
	digest := Hash(data)
	return Snapshot{Path: clean, Hash: digest, HashChunks: hashChunks(digest), Size: len(data), Exists: true}, nil
}

// ReadSnapshotInRepo returns a snapshot for a repo-relative path.
func ReadSnapshotInRepo(repoPath string, filePath string) (Snapshot, error) {
	scoped, err := openScopedFile(repoPath, filePath, false)
	if err != nil {
		return Snapshot{}, err
	}
	defer scoped.close()

	data, err := scoped.root.ReadFile(scoped.name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Snapshot{
				Path:         scoped.display,
				RepoPath:     repoPath,
				RelativePath: scoped.relative,
				Exists:       false,
			}, nil
		}
		return Snapshot{}, err
	}
	digest := Hash(data)
	return Snapshot{
		Path:         scoped.display,
		RepoPath:     repoPath,
		RelativePath: scoped.relative,
		Hash:         digest,
		HashChunks:   hashChunks(digest),
		Size:         len(data),
		Exists:       true,
	}, nil
}

func resolveText(req ReplaceRequest) (oldText string, newText string, err error) {
	if req.OldB64 != "" {
		decoded, decodeErr := base64.StdEncoding.DecodeString(req.OldB64)
		if decodeErr != nil {
			return "", "", fmt.Errorf("old_b64: invalid base64: %w", decodeErr)
		}
		oldText = string(decoded)
	} else {
		oldText = req.Old
	}
	if req.NewB64 != "" {
		decoded, decodeErr := base64.StdEncoding.DecodeString(req.NewB64)
		if decodeErr != nil {
			return "", "", fmt.Errorf("new_b64: invalid base64: %w", decodeErr)
		}
		newText = string(decoded)
	} else {
		newText = req.New
	}
	return oldText, newText, nil
}

// ValidateReplaceEncoding reports whether a request's base64 arguments decode,
// without reading or writing the file. A caller that applies several
// replacements in sequence uses it to reject a malformed encoding before the
// first one writes. It runs the decoder ApplyGuardedReplace runs, so the two
// cannot disagree about what decodes.
func ValidateReplaceEncoding(req ReplaceRequest) error {
	if _, err := resolveExpectedHash(req.ExpectedHash, req.ExpectedHashChunks); err != nil {
		return err
	}
	_, _, err := resolveText(req)
	return err
}

// hashChunks splits a hex digest into eight-character groups, the shape
// snapshot and read report as hash_chunks.
func hashChunks(hash string) []string {
	if len(hash) == 0 || len(hash)%8 != 0 {
		return nil
	}
	chunks := make([]string, 0, len(hash)/8)
	for i := 0; i < len(hash); i += 8 {
		chunks = append(chunks, hash[i:i+8])
	}
	return chunks
}

var hexDigest = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

// resolveExpectedHash joins chunked transport into the single digest the
// guard compares. Chunks that do not concatenate to a 64-character hex
// digest are a transport error, not a hash conflict; when both forms are
// set they must agree.
func resolveExpectedHash(expected string, chunks []string) (string, error) {
	if len(chunks) == 0 {
		return expected, nil
	}
	joined := strings.ToLower(strings.Join(chunks, ""))
	if !hexDigest.MatchString(joined) {
		return "", errors.New("expected_hash_chunks: must concatenate to a 64-character hex digest")
	}
	if expected != "" && expected != joined {
		return "", fmt.Errorf("expected_hash %s and expected_hash_chunks disagree", expected)
	}
	return joined, nil
}

func findBestPartialMatch(text string, old string) string {
	best := 0
	bestPos := 0
	for i := 0; i < len(text); i++ {
		j := 0
		for i+j < len(text) && j < len(old) && text[i+j] == old[j] {
			j++
		}
		if j > best {
			best = j
			bestPos = i
		}
	}
	if best == 0 {
		return ""
	}
	start := bestPos
	if start > 20 {
		start = bestPos - 20
	}
	end := bestPos + best + 40
	if end > len(text) {
		end = len(text)
	}
	return text[start:end]
}

// SpanEdit is what replacing one span in a document produced: the text to
// store, whether it differs from what was there, and — when there is nothing to
// store — why.
type SpanEdit struct {
	Text    string
	Status  string
	Changed bool
	Reason  string
}

// ReplaceUniqueSpan replaces the single occurrence of oldText with newText.
//
// It is the decision ApplyGuardedReplace makes about text, lifted out of the
// file that text came from. A caller holding a document from somewhere else — a
// Confluence page body — then answers to the same rules a model already knows
// from editing files: exactly one occurrence or none, an edit already applied
// reported as success rather than as a miss, and a near-miss quoted back so the
// caller can see what it got wrong.
func ReplaceUniqueSpan(text string, oldText string, newText string) SpanEdit {
	if strings.Contains(text, newText) && !strings.Contains(text, oldText) {
		return SpanEdit{Text: text, Status: "ok", Reason: "desired text already present"}
	}
	switch count := strings.Count(text, oldText); {
	case count == 0:
		reason := "old text not found"
		if detail := findBestPartialMatch(text, oldText); detail != "" {
			reason += fmt.Sprintf("; best partial match near: %q", detail)
		}
		return SpanEdit{Status: "conflict", Reason: reason}
	case count > 1:
		return SpanEdit{Status: "conflict", Reason: "old text is not unique"}
	}

	next := strings.Replace(text, oldText, newText, 1)
	return SpanEdit{Text: next, Status: "ok", Changed: next != text}
}

// ApplyGuardedReplace replaces one unique text span only if the file hash still matches.
// Prefer OldB64/NewB64 over Old/New when the text contains characters that are
// difficult to represent in JSON strings (e.g. Go raw string literals).
func ApplyGuardedReplace(req ReplaceRequest) (ReplaceResult, error) {
	if strings.TrimSpace(req.Path) == "" {
		return ReplaceResult{}, errors.New("path is required")
	}
	scoped, err := openScopedFile(req.RepoPath, req.Path, false)
	if err != nil {
		return ReplaceResult{}, err
	}
	defer scoped.close()
	clean := scoped.display
	expectedHash, err := resolveExpectedHash(req.ExpectedHash, req.ExpectedHashChunks)
	if err != nil {
		return ReplaceResult{}, err
	}
	if expectedHash == "" {
		return ReplaceResult{}, errors.New("expected_hash is required (set expected_hash or expected_hash_chunks)")
	}
	oldText, newText, err := resolveText(req)
	if err != nil {
		return ReplaceResult{}, err
	}
	if oldText == "" {
		return ReplaceResult{}, errors.New("old text is required (set old or old_b64)")
	}
	data, err := scoped.root.ReadFile(scoped.name)
	if err != nil {
		return ReplaceResult{}, err
	}
	oldHash := Hash(data)
	if oldHash != expectedHash {
		return ReplaceResult{Status: "conflict", Path: clean, OldHash: oldHash, Reason: "file hash changed after snapshot"}, nil
	}
	// The read tool displays LF-normalized lines, so a caller copying what
	// it saw sends LF even for a CRLF file; match and write in the file's
	// own line-ending style.
	if fileUsesCRLF(data) && !strings.Contains(oldText, "\r\n") {
		oldText = strings.ReplaceAll(oldText, "\n", "\r\n")
		newText = strings.ReplaceAll(newText, "\n", "\r\n")
	}
	edit := ReplaceUniqueSpan(string(data), oldText, newText)
	if edit.Status != "ok" {
		return ReplaceResult{Status: edit.Status, Path: clean, OldHash: oldHash, Reason: edit.Reason}, nil
	}
	if !edit.Changed {
		return ReplaceResult{Status: "ok", Path: clean, Changed: false, OldHash: oldHash, NewHash: oldHash, Reason: edit.Reason}, nil
	}
	newHash := Hash([]byte(edit.Text))
	if err := scoped.root.WriteFileAtomic(scoped.name, []byte(edit.Text), 0o600); err != nil {
		return ReplaceResult{}, err
	}
	return ReplaceResult{Status: "ok", Path: clean, Changed: true, OldHash: oldHash, NewHash: newHash}, nil
}

// fileUsesCRLF reports whether the file carries CRLF line endings; the
// read tool normalizes them away, so edits copied from displayed lines
// arrive LF regardless of the file's actual style.
func fileUsesCRLF(data []byte) bool {
	return strings.Contains(string(data), "\r\n")
}

// FileLine is one numbered line of file content.
type FileLine struct {
	Number int    `json:"n"`
	Text   string `json:"text"`
}

// FileContent holds structured file read results.
type FileContent struct {
	Path         string     `json:"path"`
	RepoPath     string     `json:"repo_path,omitempty"`
	RelativePath string     `json:"relative_path,omitempty"`
	Hash         string     `json:"hash"`
	HashChunks   []string   `json:"hash_chunks,omitempty"`
	Size         int        `json:"size"`
	Exists       bool       `json:"exists"`
	Lines        []FileLine `json:"lines"`
}

// ReadFileContent reads a file and returns structured content with line numbers.
func ReadFileContent(path string) (FileContent, error) {
	clean, err := cleanPath(path)
	if err != nil {
		return FileContent{}, err
	}
	dir, name := filepath.Split(clean)
	root, err := os.OpenRoot(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return FileContent{Path: clean, Exists: false}, nil
		}
		return FileContent{}, err
	}
	defer func() { _ = root.Close() }()
	data, err := root.ReadFile(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return FileContent{Path: clean, Exists: false}, nil
		}
		return FileContent{}, err
	}
	text := string(data)
	raw := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	// Drop trailing empty line from final newline.
	if len(raw) > 0 && raw[len(raw)-1] == "" {
		raw = raw[:len(raw)-1]
	}
	lines := make([]FileLine, 0, len(raw))
	for i, line := range raw {
		lines = append(lines, FileLine{Number: i + 1, Text: line})
	}
	digest := Hash(data)
	return FileContent{
		Path:       clean,
		Hash:       digest,
		HashChunks: hashChunks(digest),
		Size:       len(data),
		Exists:     true,
		Lines:      lines,
	}, nil
}

// ReadFileContentInRepo reads a repo-relative file and returns structured content.
func ReadFileContentInRepo(repoPath string, filePath string) (FileContent, error) {
	scoped, err := openScopedFile(repoPath, filePath, false)
	if err != nil {
		return FileContent{}, err
	}
	defer scoped.close()

	data, err := scoped.root.ReadFile(scoped.name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return FileContent{
				Path:         scoped.display,
				RepoPath:     repoPath,
				RelativePath: scoped.relative,
				Exists:       false,
			}, nil
		}
		return FileContent{}, err
	}
	text := string(data)
	raw := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(raw) > 0 && raw[len(raw)-1] == "" {
		raw = raw[:len(raw)-1]
	}
	lines := make([]FileLine, 0, len(raw))
	for i, line := range raw {
		lines = append(lines, FileLine{Number: i + 1, Text: line})
	}
	digest := Hash(data)
	return FileContent{
		Path:         scoped.display,
		RepoPath:     repoPath,
		RelativePath: scoped.relative,
		Hash:         digest,
		HashChunks:   hashChunks(digest),
		Size:         len(data),
		Exists:       true,
		Lines:        lines,
	}, nil
}

// ReadFilesFileResult is a per-file result in batch reads. RelativePath is the
// only path it carries: the batch names its repo once, and an absolute path
// would repeat that root on every file while naming nothing the repo-scoped
// operations accept back.
type ReadFilesFileResult struct {
	RelativePath  string     `json:"relative_path,omitempty"`
	Hash          string     `json:"hash,omitempty"`
	Size          int        `json:"size"`
	Exists        bool       `json:"exists"`
	Lines         []FileLine `json:"lines,omitempty"`
	Error         string     `json:"error,omitempty"`
	Truncated     bool       `json:"truncated,omitempty"`
	OmittedReason string     `json:"omitted_reason,omitempty"`
}

// ReadFilesResult holds the batch read result.
type ReadFilesResult struct {
	RepoPath      string                `json:"repo_path,omitempty"`
	Files         []ReadFilesFileResult `json:"files"`
	TotalFiles    int                   `json:"total_files"`
	ReturnedFiles int                   `json:"returned_files"`
	ReturnedBytes int                   `json:"returned_bytes"`
	Truncated     bool                  `json:"truncated,omitempty"`
}

const (
	maxReadFilesPaths      = 8
	maxReadFileBytes       = 64 * 1024
	maxReadFilesTotalBytes = 128 * 1024
)

func readFileContentInRepoLimited(repoPath string, filePath string, maxBytes int) (FileContent, bool, error) {
	scoped, err := openScopedFile(repoPath, filePath, false)
	if err != nil {
		return FileContent{}, false, err
	}
	defer scoped.close()

	info, err := scoped.root.Stat(scoped.name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return FileContent{Path: scoped.display, RepoPath: repoPath, RelativePath: scoped.relative, Exists: false}, false, nil
		}
		return FileContent{}, false, err
	}
	if !info.Mode().IsRegular() {
		return FileContent{}, false, fmt.Errorf("%q is not a regular file", scoped.relative)
	}
	content := FileContent{
		Path:         scoped.display,
		RepoPath:     repoPath,
		RelativePath: scoped.relative,
		Size:         int(info.Size()),
		Exists:       true,
	}
	if info.Size() > int64(maxBytes) {
		return content, true, nil
	}

	file, err := scoped.root.OpenFile(scoped.name, os.O_RDONLY, 0)
	if err != nil {
		return FileContent{}, false, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, int64(maxBytes)+1))
	if err != nil {
		return FileContent{}, false, err
	}
	content.Size = len(data)
	if len(data) > maxBytes {
		return content, true, nil
	}
	text := string(data)
	raw := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(raw) > 0 && raw[len(raw)-1] == "" {
		raw = raw[:len(raw)-1]
	}
	content.Lines = make([]FileLine, 0, len(raw))
	for i, line := range raw {
		content.Lines = append(content.Lines, FileLine{Number: i + 1, Text: line})
	}
	content.Hash = Hash(data)
	content.HashChunks = hashChunks(content.Hash)
	return content, false, nil
}

// ReadFilesInRepo reads multiple repo-relative files with hard bounds.
func ReadFilesInRepo(repoPath string, paths []string) (ReadFilesResult, error) {
	if len(paths) == 0 {
		return ReadFilesResult{}, errors.New("paths must not be empty")
	}
	if len(paths) > maxReadFilesPaths {
		return ReadFilesResult{}, fmt.Errorf("too many paths: %d, max %d", len(paths), maxReadFilesPaths)
	}

	result := ReadFilesResult{
		RepoPath:   repoPath,
		Files:      make([]ReadFilesFileResult, 0, len(paths)),
		TotalFiles: len(paths),
	}

	var totalBytes int
	truncated := false

	for _, path := range paths {
		fc, oversized, err := readFileContentInRepoLimited(repoPath, path, maxReadFileBytes)
		if err != nil {
			result.Files = append(result.Files, ReadFilesFileResult{
				RelativePath: filepath.ToSlash(filepath.Clean(path)),
				Error:        err.Error(),
			})
			continue
		}

		fr := ReadFilesFileResult{
			RelativePath: fc.RelativePath,
			Hash:         fc.Hash,
			Size:         fc.Size,
			Exists:       fc.Exists,
		}

		if !fc.Exists {
			fr.Error = "file does not exist"
			result.Files = append(result.Files, fr)
			continue
		}

		if oversized {
			fr.Truncated = true
			fr.OmittedReason = fmt.Sprintf("file size %d exceeds per-file limit %d", fc.Size, maxReadFileBytes)
			result.Files = append(result.Files, fr)
			truncated = true
			continue
		}

		if totalBytes+fc.Size > maxReadFilesTotalBytes {
			fr.Truncated = true
			fr.OmittedReason = fmt.Sprintf("adding %d bytes would exceed total limit %d", fc.Size, maxReadFilesTotalBytes)
			result.Files = append(result.Files, fr)
			truncated = true
			continue
		}

		fr.Lines = fc.Lines
		totalBytes += fc.Size
		result.ReturnedFiles++
		result.ReturnedBytes = totalBytes

		result.Files = append(result.Files, fr)
	}

	if truncated {
		result.Truncated = true
	}

	return result, nil
}

// Hash returns a SHA-256 hex digest for data.
func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// --- CreateIfAbsent ---

// CreateIfAbsentRequest describes a file creation that only happens if the file doesn't exist.
type CreateIfAbsentRequest struct {
	Path       string `json:"path"`
	RepoPath   string `json:"repo_path,omitempty"`
	Content    string `json:"content,omitempty"`
	ContentB64 string `json:"content_b64,omitempty"`
	Mode       int    `json:"mode,omitempty"`
}

// CreateIfAbsent creates a file with the given content only if it doesn't already exist.
// Returns already_present if the file exists, ok if created.
func CreateIfAbsent(req CreateIfAbsentRequest) (ReplaceResult, error) {
	if strings.TrimSpace(req.Path) == "" {
		return ReplaceResult{}, errors.New("path is required")
	}
	content, err := resolveContent(req.Content, req.ContentB64)
	if err != nil {
		return ReplaceResult{}, err
	}
	if content == "" {
		return ReplaceResult{}, errors.New("content is required (set content or content_b64)")
	}
	mode, err := validatedFileMode(req.Mode, 0o644)
	if err != nil {
		return ReplaceResult{}, err
	}
	scoped, err := openScopedFile(req.RepoPath, req.Path, false)
	if err != nil {
		return ReplaceResult{}, err
	}
	defer scoped.close()
	if _, statErr := scoped.root.Stat(scoped.name); statErr == nil {
		return ReplaceResult{Status: "already_present", Path: scoped.display, Changed: false, Reason: "file already exists"}, nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return ReplaceResult{}, statErr
	}
	if err := scoped.root.CreateExclusive(scoped.name, []byte(content), mode); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ReplaceResult{Status: "already_present", Path: scoped.display, Changed: false, Reason: "file already exists"}, nil
		}
		return ReplaceResult{}, err
	}
	newHash := Hash([]byte(content))
	return ReplaceResult{Status: "ok", Path: scoped.display, Changed: true, NewHash: newHash}, nil
}

// --- AppendUnique ---

// AppendUniqueRequest describes an append that only happens if the content is not already present.
type AppendUniqueRequest struct {
	Path         string `json:"path"`
	RepoPath     string `json:"repo_path,omitempty"`
	ExpectedHash string `json:"expected_hash"`
	Content      string `json:"content,omitempty"`
	ContentB64   string `json:"content_b64,omitempty"`
	Separator    string `json:"separator,omitempty"`
}

// AppendUnique appends content to the end of a file only if the exact content
// is not already present anywhere in the file. Hash-guarded.
func AppendUnique(req AppendUniqueRequest) (ReplaceResult, error) {
	if strings.TrimSpace(req.Path) == "" {
		return ReplaceResult{}, errors.New("path is required")
	}
	if req.ExpectedHash == "" {
		return ReplaceResult{}, errors.New("expected_hash is required")
	}
	scoped, err := openScopedFile(req.RepoPath, req.Path, false)
	if err != nil {
		return ReplaceResult{}, err
	}
	defer scoped.close()
	clean := scoped.display
	content, err := resolveContent(req.Content, req.ContentB64)
	if err != nil {
		return ReplaceResult{}, err
	}
	if content == "" {
		return ReplaceResult{}, errors.New("content is required (set content or content_b64)")
	}
	data, readErr := scoped.root.ReadFile(scoped.name)
	if readErr != nil {
		if errors.Is(readErr, os.ErrNotExist) {
			return ReplaceResult{Status: "conflict", Path: clean, Reason: "file does not exist"}, nil
		}
		return ReplaceResult{}, readErr
	}
	oldHash := Hash(data)
	if oldHash != req.ExpectedHash {
		return ReplaceResult{Status: "conflict", Path: clean, OldHash: oldHash, Reason: "file hash changed after snapshot"}, nil
	}
	text := string(data)
	if strings.Contains(text, content) {
		return ReplaceResult{Status: "ok", Path: clean, Changed: false, OldHash: oldHash, NewHash: oldHash, Reason: "content already present"}, nil
	}
	separator := req.Separator
	if separator == "" {
		separator = "\n"
	}
	var next string
	switch {
	case len(data) == 0:
		next = content
	case strings.HasSuffix(text, separator):
		next = text + content
	default:
		next = text + separator + content
	}
	newHash := Hash([]byte(next))
	if err := scoped.root.WriteFileAtomic(scoped.name, []byte(next), 0o600); err != nil {
		return ReplaceResult{}, err
	}
	return ReplaceResult{Status: "ok", Path: clean, Changed: true, OldHash: oldHash, NewHash: newHash}, nil
}

// --- DeleteExactBlock ---

// DeleteExactBlockRequest describes deletion of an exact multi-line block.
type DeleteExactBlockRequest struct {
	Path         string `json:"path"`
	RepoPath     string `json:"repo_path,omitempty"`
	ExpectedHash string `json:"expected_hash"`
	Block        string `json:"block,omitempty"`
	BlockB64     string `json:"block_b64,omitempty"`
}

// DeleteExactBlock removes an exact multi-line block from a file.
// If the block is not found, returns ok with changed=false (idempotent).
// If the block appears more than once, returns conflict.
func DeleteExactBlock(req DeleteExactBlockRequest) (ReplaceResult, error) {
	if strings.TrimSpace(req.Path) == "" {
		return ReplaceResult{}, errors.New("path is required")
	}
	if req.ExpectedHash == "" {
		return ReplaceResult{}, errors.New("expected_hash is required")
	}
	scoped, err := openScopedFile(req.RepoPath, req.Path, false)
	if err != nil {
		return ReplaceResult{}, err
	}
	defer scoped.close()
	clean := scoped.display
	block, err := resolveContent(req.Block, req.BlockB64)
	if err != nil {
		return ReplaceResult{}, err
	}
	if block == "" {
		return ReplaceResult{}, errors.New("block is required (set block or block_b64)")
	}
	data, readErr := scoped.root.ReadFile(scoped.name)
	if readErr != nil {
		if errors.Is(readErr, os.ErrNotExist) {
			return ReplaceResult{Status: "conflict", Path: clean, Reason: "file does not exist"}, nil
		}
		return ReplaceResult{}, readErr
	}
	oldHash := Hash(data)
	if oldHash != req.ExpectedHash {
		return ReplaceResult{Status: "conflict", Path: clean, OldHash: oldHash, Reason: "file hash changed after snapshot"}, nil
	}
	text := string(data)
	count := strings.Count(text, block)
	if count == 0 {
		return ReplaceResult{Status: "ok", Path: clean, Changed: false, OldHash: oldHash, NewHash: oldHash, Reason: "block not found (already absent)"}, nil
	}
	if count > 1 {
		return ReplaceResult{Status: "conflict", Path: clean, OldHash: oldHash, Reason: "block is not unique"}, nil
	}
	removedAt := strings.Index(text, block)
	if removedAt < 0 {
		return ReplaceResult{Status: "ok", Path: clean, Changed: false, OldHash: oldHash, NewHash: oldHash, Reason: "block not found (already absent)"}, nil
	}
	left := text[:removedAt]
	right := text[removedAt+len(block):]
	// Collapse the extra newline the deletion itself can leave at the
	// seam; runs elsewhere in the file are content, not debris.
	for strings.HasSuffix(left, "\n\n") && strings.HasPrefix(right, "\n") {
		right = strings.TrimPrefix(right, "\n")
	}
	next := left + right
	newHash := Hash([]byte(next))
	if err := scoped.root.WriteFileAtomic(scoped.name, []byte(next), 0o600); err != nil {
		return ReplaceResult{}, err
	}
	return ReplaceResult{Status: "ok", Path: clean, Changed: true, OldHash: oldHash, NewHash: newHash}, nil
}

func validatedFileMode(requested int, fallback os.FileMode) (os.FileMode, error) {
	if requested == 0 {
		return fallback, nil
	}
	if requested < 0 || requested > 0o777 {
		return 0, fmt.Errorf("mode must be between 0000 and 0777: %d", requested)
	}
	return os.FileMode(requested), nil
}

// resolveContent returns the content from plain text or base64-encoded text.
func resolveContent(plain string, b64 string) (string, error) {
	if b64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return "", fmt.Errorf("content_b64: invalid base64: %w", err)
		}
		return string(decoded), nil
	}
	return plain, nil
}

// --- WriteFile ---

// WriteFileRequest describes a file write with optional overwrite guard.
type WriteFileRequest struct {
	RepoPath     string `json:"repo_path"`
	Path         string `json:"path"`
	Content      string `json:"content,omitempty"`
	ContentB64   string `json:"content_b64,omitempty"`
	ExpectedHash string `json:"expected_hash,omitempty"`
	Mode         int    `json:"mode,omitempty"`
}

// ValidateWriteFileEncoding checks transport encoding without touching the filesystem.
func ValidateWriteFileEncoding(req WriteFileRequest) error {
	_, err := resolveContent(req.Content, req.ContentB64)
	return err
}

// WriteFile writes content to a file, creating parent directories if needed.
// If ExpectedHash is set and the file exists with a different hash, returns conflict.
// If the file already has the desired content, returns ok with changed=false (idempotent).
func WriteFile(req WriteFileRequest) (ReplaceResult, error) {
	if strings.TrimSpace(req.Path) == "" {
		return ReplaceResult{}, errors.New("path is required")
	}
	content, err := resolveContent(req.Content, req.ContentB64)
	if err != nil {
		return ReplaceResult{}, err
	}
	if content == "" {
		return ReplaceResult{}, errors.New("content is required (set content or content_b64)")
	}
	mode, err := validatedFileMode(req.Mode, 0o644)
	if err != nil {
		return ReplaceResult{}, err
	}
	scoped, err := openScopedFile(req.RepoPath, req.Path, true)
	if err != nil {
		return ReplaceResult{}, err
	}
	defer scoped.close()
	existing, readErr := scoped.root.ReadFile(scoped.name)
	if readErr == nil {
		oldHash := Hash(existing)
		if req.ExpectedHash != "" && oldHash != req.ExpectedHash {
			return ReplaceResult{Status: "conflict", Path: scoped.display, OldHash: oldHash, Reason: "file hash changed after snapshot"}, nil
		}
		newHash := Hash([]byte(content))
		if oldHash == newHash {
			return ReplaceResult{Status: "ok", Path: scoped.display, Changed: false, OldHash: oldHash, NewHash: oldHash, Reason: "content already matches"}, nil
		}
		if err := scoped.root.WriteFileAtomic(scoped.name, []byte(content), 0o600); err != nil {
			return ReplaceResult{}, err
		}
		return ReplaceResult{Status: "ok", Path: scoped.display, Changed: true, OldHash: oldHash, NewHash: newHash}, nil
	}
	if !errors.Is(readErr, os.ErrNotExist) {
		return ReplaceResult{}, readErr
	}
	if err := scoped.root.WriteFileAtomic(scoped.name, []byte(content), mode); err != nil {
		return ReplaceResult{}, err
	}
	newHash := Hash([]byte(content))
	return ReplaceResult{Status: "ok", Path: scoped.display, Changed: true, NewHash: newHash}, nil
}

// --- ListDir ---

// ListDirRequest describes a directory listing query.
type ListDirRequest struct {
	RepoPath string `json:"repo_path"`
	Path     string `json:"path,omitempty"` // repo-relative, defaults to "."
}

// DirEntry is one item in a directory listing. It carries no path of its own:
// every entry sits directly in the one directory the result already names, so
// a per-entry path would repeat that directory once per entry — on a listing
// of any size, most of the payload.
type DirEntry struct {
	Name       string `json:"name"`
	IsDir      bool   `json:"is_dir"`
	Size       int64  `json:"size,omitempty"`
	ModifiedAt string `json:"modified_at,omitempty"`
	IsSymlink  bool   `json:"is_symlink,omitempty"`
}

// ListDirResult holds structured directory listing.
type ListDirResult struct {
	Path string `json:"path"`
	// RepoPath and RelativePath report the listed directory the way read and
	// snapshot report a file, and they are what makes an entry addressable:
	// every repo-scoped operation here rejects an absolute path outright, so
	// Path alone names nothing that can be read, snapshotted or edited next.
	// RelativePath is empty for the repo root, where an entry name already is
	// the repo-relative path.
	RepoPath     string     `json:"repo_path,omitempty"`
	RelativePath string     `json:"relative_path,omitempty"`
	Entries      []DirEntry `json:"entries"`
	Total        int        `json:"total"`
}

// ListDir returns a structured directory listing.
func ListDir(req ListDirRequest) (ListDirResult, error) {
	var (
		root     *safefs.Root
		relative = "."
		display  string
		repoRel  string
		err      error
	)
	if strings.TrimSpace(req.RepoPath) != "" {
		root, err = safefs.Open(req.RepoPath)
		if err != nil {
			return ListDirResult{}, err
		}
		if strings.TrimSpace(req.Path) == "" {
			display = root.Path()
		} else {
			display, repoRel, err = repoRelativePath(req.RepoPath, req.Path)
			if err != nil {
				_ = root.Close()
				return ListDirResult{}, err
			}
			relative = filepath.FromSlash(repoRel)
		}
	} else {
		filePath := req.Path
		if strings.TrimSpace(filePath) == "" {
			filePath = "."
		}
		display, err = cleanPath(filePath)
		if err != nil {
			return ListDirResult{}, err
		}
		root, err = safefs.Open(display)
		if err != nil {
			return ListDirResult{}, err
		}
	}
	defer func() { _ = root.Close() }()

	entries, err := root.ReadDir(relative)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			nearest := nearestExistingDir(root, relative)
			return ListDirResult{}, fmt.Errorf("list path %q does not exist under repo root %q; nearest existing directory %q — list it to see available names", relative, root.Path(), nearest)
		}
		return ListDirResult{}, err
	}
	result := ListDirResult{
		Path:         display,
		RepoPath:     strings.TrimSpace(req.RepoPath),
		RelativePath: repoRel,
		Entries:      make([]DirEntry, 0, len(entries)),
	}
	for _, e := range entries {
		entry := DirEntry{
			Name:  e.Name(),
			IsDir: e.IsDir(),
		}
		if e.Type()&os.ModeSymlink != 0 {
			entry.IsSymlink = true
		}
		info, infoErr := e.Info()
		if infoErr == nil {
			entry.Size = info.Size()
			entry.ModifiedAt = info.ModTime().UTC().Format(time.RFC3339)
		}
		result.Entries = append(result.Entries, entry)
	}
	result.Total = len(result.Entries)
	return result, nil
}

const protectedLeanGenericToolMessage = "policy_denied: generic file access to protected task registry source is disabled for this path only; continue with task action=current, task action=get, task action=graph or task action=context, or use a focused search that skips protected registry files"

func rejectProtectedLeanPath(path string) error {
	if isProtectedLeanPath(path) {
		return fmt.Errorf("%s: %s", protectedLeanGenericToolMessage, filepath.ToSlash(filepath.Clean(path)))
	}
	return nil
}

func isProtectedLeanPath(path string) bool {
	clean := strings.ToLower(filepath.ToSlash(filepath.Clean(strings.TrimSpace(path))))
	if clean == "mcpaihelperproject/activetasks.lean" {
		return true
	}
	if strings.HasPrefix(clean, "mcpaihelperproject/taskregistry") && strings.HasSuffix(clean, ".lean") {
		return true
	}
	return strings.HasPrefix(clean, "tasks/") && strings.HasSuffix(clean, ".lean")
}

func isTaskRegistryRelative(relative string) bool {
	clean := strings.ToLower(path.Clean(filepath.ToSlash(relative)))
	return clean == "obsidian-tasks" || clean == "tasks" || clean == "mcpaihelperproject"
}

// nearestExistingDir names the deepest ancestor of relative that exists
// beneath root, "." when none does. Error messages carry it so a caller who
// mistyped a path learns which directory to list next instead of guessing.
func nearestExistingDir(root *safefs.Root, relative string) string {
	candidate := filepath.ToSlash(filepath.Clean(relative))
	for candidate != "." && candidate != "/" {
		if _, err := root.Stat(filepath.FromSlash(candidate)); err == nil {
			return candidate
		}
		candidate = path.Dir(candidate)
	}
	return "."
}

func cleanPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
	}
	if err := rejectProtectedLeanPath(path); err != nil {
		return "", err
	}
	clean, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if strings.Contains(clean, string(filepath.Separator)+".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid path %q", path)
	}
	return clean, nil
}

func repoRelativePath(repoPath string, filePath string) (string, string, error) {
	if strings.TrimSpace(repoPath) == "" {
		return "", "", errors.New("repo_path is required")
	}
	if strings.TrimSpace(filePath) == "" {
		return "", "", errors.New("path is required")
	}
	if filepath.IsAbs(filePath) || filepath.VolumeName(filePath) != "" {
		return "", "", fmt.Errorf("path must be repo-relative when repo_path is set: %q", filePath)
	}
	repoAbs, err := filepath.Abs(repoPath)
	if err != nil {
		return "", "", err
	}
	rel := filepath.Clean(filePath)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", "", fmt.Errorf("path escapes repo_path: %q", filePath)
	}
	if err := rejectProtectedLeanPath(rel); err != nil {
		return "", "", err
	}
	return filepath.Join(repoAbs, rel), filepath.ToSlash(rel), nil
}
