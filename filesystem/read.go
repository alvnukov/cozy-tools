package filesystem

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path"
	"strings"
	"unicode/utf8"
)

// Read returns a bounded snapshot while hashing the file's original raw bytes.
func (s *Service) Read(ctx context.Context, request ReadRequest) (ReadResult, error) {
	var result ReadResult
	done, err := s.begin(ctx, "read")
	if err != nil {
		return result, err
	}
	defer done()

	name, err := cleanPath(request.Path, false)
	if err != nil {
		return result, err
	}
	offset := request.Offset
	if offset == 0 {
		offset = 1
	}
	if offset < 1 {
		return result, newError(CodeInvalidInput, "read", name, "offset must be 1-based", nil)
	}
	lineLimit, err := requestInt("limit", s.limits.ReadLines, request.Limit)
	if err != nil {
		return result, err
	}
	byteLimit, err := requestInt64("max_bytes", s.limits.ReadBytes, request.MaxBytes)
	if err != nil {
		return result, err
	}

	info, err := s.root.Stat(name)
	if err != nil {
		return result, rootError("read", name, err)
	}
	if !info.Mode().IsRegular() {
		return result, newError(CodeInvalidInput, "read", name, "path is not a regular file", nil)
	}
	if info.Size() > s.limits.WholeFileBytes {
		return result, newError(CodeLimit, "read", name, fmt.Sprintf("file size %d exceeds whole-file cap %d", info.Size(), s.limits.WholeFileBytes), nil)
	}
	file, err := s.root.Open(name)
	if err != nil {
		return result, rootError("read", name, err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, s.limits.WholeFileBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return result, rootError("read", name, readErr)
	}
	if closeErr != nil {
		return result, rootError("read", name, closeErr)
	}
	if int64(len(raw)) > s.limits.WholeFileBytes {
		return result, newError(CodeLimit, "read", name, "file grew beyond whole-file cap while reading", nil)
	}
	if err := checkContext(ctx); err != nil {
		return result, err
	}

	hash := hashBytes(raw)
	result.File = metadata(name, info)
	result.SHA256 = hash
	result.HashChunks = hashChunks(hash)
	result.Binary = isBinary(raw)
	result.Offset = offset
	result.Lines = make([]Line, 0)
	if !result.Binary {
		result.Newline = newlineConvention(raw)
		result.FinalNewline = bytes.HasSuffix(raw, []byte("\n"))
	}
	if result.Binary {
		start := offset - 1
		if start > len(raw) {
			start = len(raw)
		}
		end := start + int(byteLimit)
		if end > len(raw) {
			end = len(raw)
		}
		result.Data = append([]byte(nil), raw[start:end]...)
		result.Truncation.ReturnedBytes = int64(len(result.Data))
		if end < len(raw) {
			result.NextOffset = end + 1
			result.Truncation = Truncation{Truncated: true, Reason: "byte limit", Continuation: fmt.Sprintf("offset=%d", end+1), ReturnedBytes: int64(len(result.Data))}
		}
		return result, nil
	}

	normalized := normalizeLF(raw)
	texts := splitTextLines(normalized)
	result.TotalLines = len(texts)
	if offset > len(texts) {
		return result, nil
	}
	used := int64(0)
	endIndex := offset - 1
	for endIndex < len(texts) && len(result.Lines) < lineLimit {
		text := texts[endIndex]
		cost := int64(len(text))
		if len(result.Lines) > 0 {
			cost++
		}
		if used+cost > byteLimit {
			break
		}
		result.Lines = append(result.Lines, Line{Number: endIndex + 1, Anchor: lineAnchor(text), Text: text})
		used += cost
		endIndex++
	}
	parts := make([]string, len(result.Lines))
	for i := range result.Lines {
		parts[i] = result.Lines[i].Text
	}
	result.Content = strings.Join(parts, "\n")
	result.Truncation.ReturnedItems = len(result.Lines)
	result.Truncation.ReturnedBytes = used
	if endIndex < len(texts) {
		reason := "line limit"
		if len(result.Lines) < lineLimit {
			reason = "byte limit (next line would be split)"
		}
		result.NextOffset = endIndex + 1
		result.Truncation = Truncation{
			Truncated: true, Reason: reason,
			Continuation:  fmt.Sprintf("offset=%d", result.NextOffset),
			ReturnedItems: len(result.Lines), ReturnedBytes: used,
		}
	}
	return result, nil
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hashChunks(hash string) []string {
	chunks := make([]string, 0, (len(hash)+7)/8)
	for len(hash) > 0 {
		n := 8
		if len(hash) < n {
			n = len(hash)
		}
		chunks = append(chunks, hash[:n])
		hash = hash[n:]
	}
	return chunks
}

func normalizeLF(raw []byte) []byte { return bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n")) }

func splitTextLines(raw []byte) []string {
	if len(raw) == 0 {
		return []string{}
	}
	parts := strings.Split(string(raw), "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func isBinary(raw []byte) bool {
	return bytes.IndexByte(raw, 0) >= 0 || !utf8.Valid(raw)
}

func lineAnchor(text string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(text))
	value := h.Sum64() % (26 * 26 * 26)
	return string([]byte{
		byte('a' + value/(26*26)),
		byte('a' + (value/26)%26),
		byte('a' + value%26),
	})
}

func metadata(name string, info os.FileInfo) FileMetadata {
	typeName := "file"
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		typeName = "symlink"
	case info.IsDir():
		typeName = "directory"
	case !info.Mode().IsRegular():
		typeName = "other"
	}
	return FileMetadata{
		Path: name, Name: path.Base(name), Type: typeName,
		Size: info.Size(), Mode: info.Mode(), ModTime: info.ModTime().UTC().Format("2006-01-02T15:04:05.000000000Z07:00"),
	}
}
