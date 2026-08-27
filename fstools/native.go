package fstools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/alvnukov/cozy-tools/filesystem"
	"github.com/alvnukov/cozy-tools/tool"
)

type nativeReadInput struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

func (a adapter) nativeRead(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	var request nativeReadInput
	if err := decodeStrict(input, &request); err != nil {
		return tool.Result{}, err
	}
	if err := require(request.Path, "path"); err != nil {
		return tool.Result{}, err
	}
	return a.run(ctx, "", func(service *filesystem.Service) (tool.Result, error) {
		result, err := service.Read(ctx, filesystem.ReadRequest{Path: request.Path, Offset: request.Offset, Limit: request.Limit})
		if err != nil {
			return tool.Result{}, err
		}
		rendered := renderNativeRead(result)
		return tool.Result{Content: rendered, Output: rendered, Structured: result, Detail: fmt.Sprintf("read %s", result.File.Path)}, nil
	})
}

func renderNativeRead(result filesystem.ReadResult) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "@file %s#%s\n", result.File.Path, tag(result.SHA256))
	switch {
	case result.Binary:
		if len(result.Data) == 0 {
			builder.WriteString("[binary file: no bytes returned]\n")
		} else {
			builder.WriteString("[binary base64]\n")
			builder.WriteString(base64.StdEncoding.EncodeToString(result.Data))
			builder.WriteByte('\n')
		}
	case len(result.Lines) == 0:
		builder.WriteString("[empty result]\n")
	default:
		for _, line := range result.Lines {
			fmt.Fprintf(&builder, "%d#%s|%s\n", line.Number, line.Anchor, line.Text)
		}
	}
	if result.Truncation.Truncated {
		fmt.Fprintf(&builder, "[truncated: %s", result.Truncation.Reason)
		if result.NextOffset > 0 {
			fmt.Fprintf(&builder, "; next offset=%d", result.NextOffset)
		}
		builder.WriteString("]\n")
	}
	return strings.TrimSuffix(builder.String(), "\n")
}

type nativeListInput struct {
	Path       string `json:"path"`
	MaxDepth   int    `json:"max_depth"`
	Limit      int    `json:"limit"`
	ShowHidden bool   `json:"show_hidden"`
}

func (a adapter) nativeList(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	var request nativeListInput
	if err := decodeStrict(input, &request); err != nil {
		return tool.Result{}, err
	}
	return a.run(ctx, "", func(service *filesystem.Service) (tool.Result, error) {
		result, err := service.List(ctx, filesystem.ListRequest{Path: request.Path, MaxDepth: request.MaxDepth, MaxEntries: request.Limit, ShowHidden: request.ShowHidden})
		if err != nil {
			return tool.Result{}, err
		}
		rendered := result.Tree
		if result.Truncation.Truncated {
			rendered += fmt.Sprintf("\n[truncated: %s]", result.Truncation.Reason)
		}
		return tool.Result{Content: rendered, Output: rendered, Structured: result, Detail: fmt.Sprintf("listed %s", result.Root)}, nil
	})
}

type nativeFindInput struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path"`
	Limit      int    `json:"limit"`
	ShowHidden bool   `json:"show_hidden"`
}

func (a adapter) nativeFind(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	var request nativeFindInput
	if err := decodeStrict(input, &request); err != nil {
		return tool.Result{}, err
	}
	if err := require(request.Pattern, "pattern"); err != nil {
		return tool.Result{}, err
	}
	return a.run(ctx, "", func(service *filesystem.Service) (tool.Result, error) {
		result, err := service.Find(ctx, filesystem.FindRequest{Path: request.Path, Pattern: request.Pattern, Limit: request.Limit, ShowHidden: request.ShowHidden})
		if err != nil {
			return tool.Result{}, err
		}
		rendered := strings.Join(result.Paths, "\n")
		if len(result.Paths) == 0 {
			rendered = "[no matches]"
		}
		if result.Truncation.Truncated {
			rendered += fmt.Sprintf("\n[truncated: %s]", result.Truncation.Reason)
		}
		return tool.Result{Content: rendered, Output: rendered, Structured: result, Detail: fmt.Sprintf("found %d paths", len(result.Paths))}, nil
	})
}

type nativeGrepInput struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path"`
	Glob       string `json:"glob"`
	Literal    bool   `json:"literal"`
	IgnoreCase bool   `json:"ignore_case"`
	Before     int    `json:"before"`
	After      int    `json:"after"`
	Context    int    `json:"context"`
	Limit      int    `json:"limit"`
}

func (a adapter) nativeGrep(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	var request nativeGrepInput
	if err := decodeStrict(input, &request); err != nil {
		return tool.Result{}, err
	}
	if err := require(request.Pattern, "pattern"); err != nil {
		return tool.Result{}, err
	}
	if request.Context < 0 {
		return tool.Result{}, invalid("context must not be negative")
	}
	before, after := request.Before, request.After
	if request.Context > 0 {
		if before == 0 {
			before = request.Context
		}
		if after == 0 {
			after = request.Context
		}
	}
	return a.run(ctx, "", func(service *filesystem.Service) (tool.Result, error) {
		result, err := service.Search(ctx, filesystem.SearchRequest{
			Path: request.Path, Pattern: request.Pattern, Glob: request.Glob,
			Literal: request.Literal, CaseInsensitive: request.IgnoreCase,
			Before: before, After: after, Limit: request.Limit,
		})
		if err != nil {
			return tool.Result{}, err
		}
		rendered, rendererTruncated := renderNativeGrep(result, a.modelBytes)
		if rendererTruncated && !result.Truncation.Truncated {
			result.Truncation.Truncated = true
			result.Truncation.Reason = "renderer byte limit"
		}
		return tool.Result{Content: rendered, Output: rendered, Structured: result, Detail: fmt.Sprintf("matched %d lines", len(result.Matches))}, nil
	})
}

type grepFile struct {
	path   string
	hash   string
	lines  map[int]filesystem.ContextLine
	orders []int
}

func renderNativeGrep(result filesystem.SearchResult, budget int) (string, bool) {
	files := make(map[string]*grepFile)
	var paths []string
	for _, match := range result.Matches {
		file := files[match.Path]
		if file == nil {
			file = &grepFile{path: match.Path, hash: match.SHA256, lines: make(map[int]filesystem.ContextLine)}
			files[match.Path] = file
			paths = append(paths, match.Path)
		}
		for _, line := range match.Before {
			file.lines[line.Number] = line
		}
		file.lines[match.Line] = filesystem.ContextLine{Number: match.Line, Anchor: match.Anchor, Text: match.Text}
		for _, line := range match.After {
			file.lines[line.Number] = line
		}
	}
	sort.Strings(paths)
	const marker = "[truncated: renderer byte limit]"
	payloadBudget := budget - len(marker) - 1
	if payloadBudget < 0 {
		payloadBudget = 0
	}
	var builder strings.Builder
	appendChunk := func(chunk string) bool {
		if builder.Len()+len(chunk) > payloadBudget {
			return false
		}
		builder.WriteString(chunk)
		return true
	}
	if len(paths) == 0 {
		if result.Truncation.Truncated {
			return fmt.Sprintf("[no matches]\n[truncated: %s]", result.Truncation.Reason), false
		}
		return "[no matches]", false
	}
	for pathIndex, path := range paths {
		file := files[path]
		if pathIndex > 0 && !appendChunk("\n") {
			return strings.TrimSuffix(builder.String(), "\n") + "\n" + marker, true
		}
		if !appendChunk(fmt.Sprintf("@file %s#%s\n", file.path, tag(file.hash))) {
			return strings.TrimSuffix(builder.String(), "\n") + "\n" + marker, true
		}
		file.orders = file.orders[:0]
		for number := range file.lines {
			file.orders = append(file.orders, number)
		}
		sort.Ints(file.orders)
		for _, number := range file.orders {
			line := file.lines[number]
			if !appendChunk(fmt.Sprintf("%d#%s|%s\n", line.Number, line.Anchor, line.Text)) {
				return strings.TrimSuffix(builder.String(), "\n") + "\n" + marker, true
			}
		}
	}
	rendered := strings.TrimSuffix(builder.String(), "\n")
	if result.Truncation.Truncated {
		truncation := fmt.Sprintf("[truncated: %s]", result.Truncation.Reason)
		if len(rendered)+1+len(truncation) <= budget {
			rendered += "\n" + truncation
		}
	}
	return rendered, false
}

type nativeWriteInput struct {
	Path    string  `json:"path"`
	Content *string `json:"content"`
}

func (a adapter) nativeWrite(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	var request nativeWriteInput
	if err := decodeStrict(input, &request); err != nil {
		return tool.Result{}, err
	}
	if err := require(request.Path, "path"); err != nil {
		return tool.Result{}, err
	}
	if request.Content == nil {
		return tool.Result{}, invalid("content is required")
	}
	return a.run(ctx, "", func(service *filesystem.Service) (tool.Result, error) {
		result, err := service.Write(ctx, filesystem.WriteRequest{Path: request.Path, Content: *request.Content})
		if err != nil {
			return tool.Result{}, err
		}
		rendered := fmt.Sprintf("@file %s#%s\n%s; sha256=%s; bytes=%d", result.Path, tag(result.NewSHA256), result.Status, result.NewSHA256, result.Bytes)
		return tool.Result{Content: rendered, Output: rendered, Structured: result, Detail: result.Status + " " + result.Path}, nil
	})
}

type nativeEditInput struct {
	Path  string `json:"path"`
	Hash  string `json:"hash"`
	Edits []struct {
		From    string  `json:"from"`
		To      string  `json:"to"`
		Content *string `json:"content"`
	} `json:"edits"`
}

func (a adapter) nativeEdit(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	var request nativeEditInput
	if err := decodeStrict(input, &request); err != nil {
		return tool.Result{}, err
	}
	if err := require(request.Path, "path"); err != nil {
		return tool.Result{}, err
	}
	if err := require(request.Hash, "hash"); err != nil {
		return tool.Result{}, err
	}
	if len(request.Edits) == 0 {
		return tool.Result{}, invalid("edits must contain at least one edit")
	}
	edits := make([]filesystem.HashlineEdit, len(request.Edits))
	for index, edit := range request.Edits {
		if edit.From == "" {
			return tool.Result{}, invalid(fmt.Sprintf("edits[%d].from is required", index))
		}
		edits[index] = filesystem.HashlineEdit{From: edit.From, To: edit.To, Content: edit.Content}
	}
	return a.run(ctx, "", func(service *filesystem.Service) (tool.Result, error) {
		result, err := service.Edit(ctx, filesystem.EditRequest{Path: request.Path, ExpectedSHA256: request.Hash, Edits: edits})
		if err != nil {
			return tool.Result{}, err
		}
		rendered := fmt.Sprintf("@file %s#%s\n%s; sha256=%s\nRe-read before editing again; prior TAG and line anchors are stale.", result.Path, tag(result.NewSHA256), result.Status, result.NewSHA256)
		return tool.Result{Content: rendered, Output: rendered, Structured: result, Detail: result.Status + " " + result.Path}, nil
	})
}
