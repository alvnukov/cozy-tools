package fstools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alvnukov/cozy-tools/filesystem"
	"github.com/alvnukov/cozy-tools/tool"
)

type mcpFileInput struct {
	RepoPath     string   `json:"repo_path"`
	Action       string   `json:"action"`
	Path         string   `json:"path"`
	Paths        []string `json:"paths"`
	Offset       int      `json:"offset"`
	Limit        int      `json:"limit"`
	MaxDepth     int      `json:"max_depth"`
	ShowHidden   bool     `json:"show_hidden"`
	Pattern      string   `json:"pattern"`
	Regex        bool     `json:"regex"`
	Literal      bool     `json:"literal"`
	IgnoreCase   bool     `json:"ignore_case"`
	Glob         string   `json:"glob"`
	Globs        []string `json:"globs"`
	Before       int      `json:"before"`
	After        int      `json:"after"`
	Context      int      `json:"context"`
	Content      *string  `json:"content"`
	ContentB64   *string  `json:"content_b64"`
	ExpectedHash string   `json:"expected_hash"`
}

type mcpReadData struct {
	Path         string                `json:"path"`
	SHA256       string                `json:"sha256"`
	HashChunks   []string              `json:"hash_chunks"`
	Size         int64                 `json:"size"`
	Binary       bool                  `json:"binary"`
	Content      *string               `json:"content,omitempty"`
	ContentB64   *string               `json:"content_b64,omitempty"`
	Lines        []filesystem.Line     `json:"lines,omitempty"`
	TotalLines   int                   `json:"total_lines"`
	Offset       int                   `json:"offset"`
	NextOffset   int                   `json:"next_offset,omitempty"`
	Newline      string                `json:"newline,omitempty"`
	FinalNewline bool                  `json:"final_newline,omitempty"`
	Truncation   filesystem.Truncation `json:"truncation"`
	raw          []byte
}

type mcpReadManyData struct {
	Files     []mcpReadData `json:"files"`
	Requested int           `json:"requested"`
	Returned  int           `json:"returned"`
	Truncated bool          `json:"truncated"`
	Reason    string        `json:"reason,omitempty"`
}

type mcpListData struct {
	Root       string                `json:"root"`
	Entries    []filesystem.Entry    `json:"entries"`
	Truncation filesystem.Truncation `json:"truncation"`
}

type mcpMutationData struct {
	Path       string   `json:"path"`
	OldSHA256  string   `json:"old_sha256,omitempty"`
	NewSHA256  string   `json:"new_sha256"`
	HashChunks []string `json:"hash_chunks"`
	Changed    bool     `json:"changed"`
	Status     string   `json:"status"`
	Bytes      int64    `json:"bytes,omitempty"`
}

func (a adapter) mcpFile(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	var request mcpFileInput
	if err := decodeStrict(input, &request); err != nil {
		return tool.Result{}, err
	}
	if err := require(request.RepoPath, "repo_path"); err != nil {
		return tool.Result{}, err
	}
	if err := validateMCPFileInput(request); err != nil {
		return tool.Result{}, err
	}
	return a.run(ctx, request.RepoPath, func(service *filesystem.Service) (tool.Result, error) {
		switch request.Action {
		case "read":
			return a.mcpRead(ctx, service, request)
		case "read_many":
			return a.mcpReadMany(ctx, service, request)
		case "search":
			return a.mcpSearch(ctx, service, request)
		case "list":
			return a.mcpList(ctx, service, request)
		case "write", "create":
			return a.mcpWrite(ctx, service, request)
		default:
			return tool.Result{}, invalid(fmt.Sprintf("unknown action %q", request.Action))
		}
	})
}

func validateMCPFileInput(request mcpFileInput) error {
	if err := require(request.Action, "action"); err != nil {
		return err
	}
	switch request.Action {
	case "read":
		return require(request.Path, "path")
	case "read_many":
		if len(request.Paths) == 0 {
			return invalid("paths must contain at least one path")
		}
		if len(request.Paths) > maxReadManyFiles {
			return invalid(fmt.Sprintf("paths may contain at most %d files", maxReadManyFiles))
		}
		for index, path := range request.Paths {
			if path == "" {
				return invalid(fmt.Sprintf("paths[%d] must not be empty", index))
			}
		}
		return nil
	case "search":
		if err := require(request.Pattern, "pattern"); err != nil {
			return err
		}
		if request.Regex && request.Literal {
			return invalid("regex and literal cannot both be true")
		}
		if request.Context < 0 {
			return invalid("context must not be negative")
		}
		return nil
	case "list":
		return nil
	case "write", "create":
		if err := require(request.Path, "path"); err != nil {
			return err
		}
		if (request.Content == nil) == (request.ContentB64 == nil) {
			return invalid("exactly one of content or content_b64 is required")
		}
		if request.Action == "create" && request.ExpectedHash != "" {
			return invalid("create does not accept expected_hash")
		}
		return nil
	default:
		return invalid(fmt.Sprintf("unknown action %q", request.Action))
	}
}

func (a adapter) mcpRead(ctx context.Context, service *filesystem.Service, request mcpFileInput) (tool.Result, error) {
	read, err := service.Read(ctx, filesystem.ReadRequest{Path: request.Path, Offset: request.Offset, Limit: request.Limit})
	if err != nil {
		return tool.Result{}, err
	}
	structured := makeMCPReadData(read)
	fitMCPRead(&structured, a.modelBytes)
	content := fmt.Sprintf("read %s (%d bytes, sha256 %s)", structured.Path, structured.Size, structured.SHA256)
	if structured.Truncation.Truncated {
		content += "; truncated: " + structured.Truncation.Reason
	}
	return tool.Result{Content: content, Structured: structured, Detail: "read " + structured.Path}, nil
}

func makeMCPReadData(read filesystem.ReadResult) mcpReadData {
	result := mcpReadData{
		Path: read.File.Path, SHA256: read.SHA256, HashChunks: append([]string(nil), read.HashChunks...),
		Size: read.File.Size, Binary: read.Binary, Lines: append([]filesystem.Line(nil), read.Lines...),
		TotalLines: read.TotalLines, Offset: read.Offset, NextOffset: read.NextOffset,
		Newline: read.Newline, FinalNewline: read.FinalNewline, Truncation: read.Truncation,
	}
	if read.Binary {
		result.raw = append([]byte(nil), read.Data...)
		encoded := base64.StdEncoding.EncodeToString(result.raw)
		result.ContentB64 = &encoded
	} else {
		content := read.Content
		result.Content = &content
	}
	return result
}

func fitMCPRead(result *mcpReadData, budget int) bool {
	// Truncation metadata is attached after the loop; reserve room for it so
	// the marshalled result stays within budget once it is set.
	const truncationHeadroom = 128
	if budget > truncationHeadroom {
		budget -= truncationHeadroom
	}
	truncated := false
	for jsonSize(result) > budget {
		truncated = true
		if result.Binary && len(result.raw) > 0 {
			over := jsonSize(result) - budget
			remove := over*3/4 + 1
			if remove > len(result.raw) {
				remove = len(result.raw)
			}
			result.raw = result.raw[:len(result.raw)-remove]
			encoded := base64.StdEncoding.EncodeToString(result.raw)
			result.ContentB64 = &encoded
			result.NextOffset = result.Offset + len(result.raw)
			continue
		}
		if !result.Binary && len(result.Lines) > 0 {
			removed := result.Lines[len(result.Lines)-1]
			result.Lines = result.Lines[:len(result.Lines)-1]
			parts := make([]string, len(result.Lines))
			for index := range result.Lines {
				parts[index] = result.Lines[index].Text
			}
			content := strings.Join(parts, "\n")
			result.Content = &content
			result.NextOffset = removed.Number
			continue
		}
		break
	}
	if truncated {
		result.Truncation.Truncated = true
		result.Truncation.Reason = "model byte budget"
		result.Truncation.Continuation = fmt.Sprintf("offset=%d", result.NextOffset)
		result.Truncation.ReturnedItems = len(result.Lines)
		if result.Binary {
			result.Truncation.ReturnedBytes = int64(len(result.raw))
		}
	}
	return truncated
}

func (a adapter) mcpReadMany(ctx context.Context, service *filesystem.Service, request mcpFileInput) (tool.Result, error) {
	result := mcpReadManyData{Files: make([]mcpReadData, 0, len(request.Paths)), Requested: len(request.Paths)}
	payloadBudget := a.modelBytes - 512
	for _, path := range request.Paths {
		read, err := service.Read(ctx, filesystem.ReadRequest{Path: path, Offset: request.Offset, Limit: request.Limit})
		if err != nil {
			return tool.Result{}, err
		}
		item := makeMCPReadData(read)
		remaining := payloadBudget - jsonSize(result)
		if remaining < 512 {
			result.Truncated = true
			result.Reason = "aggregate model byte budget"
			break
		}
		fitMCPRead(&item, remaining)
		candidate := result
		candidate.Files = append(append([]mcpReadData(nil), result.Files...), item)
		candidate.Returned = len(candidate.Files)
		if jsonSize(candidate) > payloadBudget {
			result.Truncated = true
			result.Reason = "aggregate model byte budget"
			break
		}
		result.Files = append(result.Files, item)
		result.Returned = len(result.Files)
	}
	if result.Returned < result.Requested {
		result.Truncated = true
		if result.Reason == "" {
			result.Reason = "aggregate model byte budget"
		}
	}
	content := fmt.Sprintf("read %d/%d files", result.Returned, result.Requested)
	if result.Truncated {
		content += "; truncated: " + result.Reason
	}
	return tool.Result{Content: content, Structured: result, Detail: content}, nil
}

func (a adapter) mcpSearch(ctx context.Context, service *filesystem.Service, request mcpFileInput) (tool.Result, error) {
	before, after := request.Before, request.After
	if request.Context > 0 {
		if before == 0 {
			before = request.Context
		}
		if after == 0 {
			after = request.Context
		}
	}
	result, err := service.Search(ctx, filesystem.SearchRequest{
		Path: request.Path, Pattern: request.Pattern, Literal: request.Literal,
		CaseInsensitive: request.IgnoreCase, Glob: request.Glob, Globs: request.Globs,
		ShowHidden: request.ShowHidden, Before: before, After: after, Limit: request.Limit,
	})
	if err != nil {
		return tool.Result{}, err
	}
	for jsonSize(result) > a.modelBytes && len(result.Matches) > 0 {
		result.Matches = result.Matches[:len(result.Matches)-1]
		result.Truncation.Truncated = true
		result.Truncation.Reason = "model byte budget"
		result.Truncation.Continuation = fmt.Sprintf("match_after=%d", len(result.Matches))
		result.Truncation.ReturnedItems = len(result.Matches)
	}
	content := fmt.Sprintf("matched %d lines", len(result.Matches))
	if result.Truncation.Truncated {
		content += "; truncated: " + result.Truncation.Reason
	}
	return tool.Result{Content: content, Structured: result, Detail: content}, nil
}

func (a adapter) mcpList(ctx context.Context, service *filesystem.Service, request mcpFileInput) (tool.Result, error) {
	listed, err := service.List(ctx, filesystem.ListRequest{Path: request.Path, MaxDepth: request.MaxDepth, MaxEntries: request.Limit, ShowHidden: request.ShowHidden})
	if err != nil {
		return tool.Result{}, err
	}
	result := mcpListData{Root: listed.Root, Entries: listed.Entries, Truncation: listed.Truncation}
	for jsonSize(result) > a.modelBytes && len(result.Entries) > 0 {
		result.Entries = result.Entries[:len(result.Entries)-1]
		result.Truncation.Truncated = true
		result.Truncation.Reason = "model byte budget"
		result.Truncation.Continuation = fmt.Sprintf("entry_after=%d", len(result.Entries))
		result.Truncation.ReturnedItems = len(result.Entries)
	}
	content := fmt.Sprintf("listed %s: %d entries", result.Root, len(result.Entries))
	if result.Truncation.Truncated {
		content += "; truncated: " + result.Truncation.Reason
	}
	return tool.Result{Content: content, Structured: result, Output: listed.Tree, Detail: content}, nil
}

func (a adapter) mcpWrite(ctx context.Context, service *filesystem.Service, request mcpFileInput) (tool.Result, error) {
	write := filesystem.WriteRequest{Path: request.Path, ExpectedSHA256: request.ExpectedHash}
	if request.ContentB64 != nil {
		decoded, err := base64.StdEncoding.DecodeString(*request.ContentB64)
		if err != nil {
			return tool.Result{}, tool.WrapError(tool.CodeInvalidArgument, "content_b64 must be valid standard base64", err)
		}
		write.Data = append([]byte{}, decoded...)
	} else {
		write.Content = *request.Content
	}
	if request.Action == "create" {
		write.Mode = filesystem.WriteCreateOnly
	}
	result, err := service.Write(ctx, write)
	if err != nil {
		return tool.Result{}, err
	}
	structured := mutationFromWrite(result)
	content := fmt.Sprintf("%s %s (%d bytes, sha256 %s)", result.Status, result.Path, result.Bytes, result.NewSHA256)
	return tool.Result{Content: content, Structured: structured, Detail: result.Status + " " + result.Path}, nil
}

func mutationFromWrite(result filesystem.WriteResult) mcpMutationData {
	return mcpMutationData{
		Path: result.Path, OldSHA256: result.OldSHA256, NewSHA256: result.NewSHA256,
		HashChunks: splitHash(result.NewSHA256), Changed: result.Changed, Status: result.Status, Bytes: result.Bytes,
	}
}

func jsonSize(value any) int {
	encoded, err := json.Marshal(value)
	if err != nil {
		return int(^uint(0) >> 1)
	}
	return len(encoded)
}

func splitHash(hash string) []string {
	chunks := make([]string, 0, (len(hash)+7)/8)
	for len(hash) > 0 {
		length := 8
		if len(hash) < length {
			length = len(hash)
		}
		chunks = append(chunks, hash[:length])
		hash = hash[length:]
	}
	return chunks
}
