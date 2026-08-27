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

type mcpEditInput struct {
	RepoPath     string  `json:"repo_path"`
	Path         string  `json:"path"`
	Action       string  `json:"action"`
	OldText      *string `json:"old_text"`
	NewText      *string `json:"new_text"`
	Content      *string `json:"content"`
	ContentB64   *string `json:"content_b64"`
	ExpectedHash string  `json:"expected_hash"`
}

func (a adapter) mcpEdit(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	var request mcpEditInput
	if err := decodeStrict(input, &request); err != nil {
		return tool.Result{}, err
	}
	if err := validateMCPEditInput(request); err != nil {
		return tool.Result{}, err
	}
	return a.run(ctx, request.RepoPath, func(service *filesystem.Service) (tool.Result, error) {
		if request.Action == "create_if_absent" {
			return mcpCreateIfAbsent(ctx, service, request)
		}
		var edit filesystem.EditRequest
		switch request.Action {
		case "replace":
			edit = filesystem.EditRequest{
				Path: request.Path, ExpectedSHA256: request.ExpectedHash,
				Replace: &filesystem.ReplaceEdit{Old: *request.OldText, New: *request.NewText},
			}
		case "delete_exact":
			edit = filesystem.EditRequest{
				Path: request.Path, ExpectedSHA256: request.ExpectedHash,
				Replace: &filesystem.ReplaceEdit{Old: *request.Content, New: ""},
			}
		case "append_unique":
			var err error
			edit, err = appendUniqueRequest(ctx, service, request)
			if err != nil {
				return tool.Result{}, err
			}
		}
		result, err := service.Edit(ctx, edit)
		if err != nil {
			return tool.Result{}, err
		}
		structured := mcpMutationData{
			Path: result.Path, OldSHA256: result.OldSHA256, NewSHA256: result.NewSHA256,
			HashChunks: splitHash(result.NewSHA256), Changed: result.Changed, Status: result.Status,
		}
		content := fmt.Sprintf("%s %s (sha256 %s)", result.Status, result.Path, result.NewSHA256)
		return tool.Result{Content: content, Structured: structured, Detail: result.Status + " " + result.Path}, nil
	})
}

func validateMCPEditInput(request mcpEditInput) error {
	if err := require(request.RepoPath, "repo_path"); err != nil {
		return err
	}
	if err := require(request.Path, "path"); err != nil {
		return err
	}
	if err := require(request.Action, "action"); err != nil {
		return err
	}
	switch request.Action {
	case "replace":
		if request.OldText == nil || *request.OldText == "" {
			return invalid("old_text must be a non-empty string for replace")
		}
		if request.NewText == nil {
			return invalid("new_text is required for replace")
		}
		return require(request.ExpectedHash, "expected_hash")
	case "append_unique", "delete_exact":
		if request.Content == nil || *request.Content == "" {
			return invalid("content must be a non-empty string for " + request.Action)
		}
		if request.ContentB64 != nil {
			return invalid(request.Action + " accepts text content only")
		}
		return require(request.ExpectedHash, "expected_hash")
	case "create_if_absent":
		if (request.Content == nil) == (request.ContentB64 == nil) {
			return invalid("exactly one of content or content_b64 is required")
		}
		if request.ExpectedHash != "" {
			return invalid("create_if_absent does not accept expected_hash")
		}
		return nil
	default:
		return invalid(fmt.Sprintf("unknown action %q", request.Action))
	}
}

func appendUniqueRequest(ctx context.Context, service *filesystem.Service, request mcpEditInput) (filesystem.EditRequest, error) {
	read, err := service.Read(ctx, filesystem.ReadRequest{Path: request.Path})
	if err != nil {
		return filesystem.EditRequest{}, err
	}
	if read.Binary {
		return filesystem.EditRequest{}, invalid("append_unique cannot edit a binary file")
	}
	if read.Truncation.Truncated || len(read.Lines) != read.TotalLines {
		return filesystem.EditRequest{}, tool.NewError(tool.CodeLimitExceeded, "append_unique requires a complete snapshot within configured read limits")
	}
	snapshotParts := make([]string, len(read.Lines))
	for index := range read.Lines {
		snapshotParts[index] = read.Lines[index].Text
	}
	snapshot := strings.Join(snapshotParts, "\n")
	addition := strings.ReplaceAll(*request.Content, "\r\n", "\n")
	identity := strings.TrimSuffix(addition, "\n")
	if identity == "" {
		return filesystem.EditRequest{}, invalid("append_unique content must contain non-newline text")
	}
	var replacement filesystem.ReplaceEdit
	if strings.Contains(snapshot, identity) {
		replacement = filesystem.ReplaceEdit{Old: identity, New: identity}
	} else {
		appended := snapshot
		if read.FinalNewline {
			appended += "\n"
		}
		appended += addition
		if read.FinalNewline {
			appended = strings.TrimSuffix(appended, "\n")
		}
		replacement = filesystem.ReplaceEdit{Old: snapshot, New: appended}
	}
	return filesystem.EditRequest{
		Path: request.Path, ExpectedSHA256: request.ExpectedHash, Replace: &replacement,
	}, nil
}

func mcpCreateIfAbsent(ctx context.Context, service *filesystem.Service, request mcpEditInput) (tool.Result, error) {
	write := filesystem.WriteRequest{Path: request.Path, Mode: filesystem.WriteCreateOnly}
	if request.ContentB64 != nil {
		decoded, err := base64.StdEncoding.DecodeString(*request.ContentB64)
		if err != nil {
			return tool.Result{}, tool.WrapError(tool.CodeInvalidArgument, "content_b64 must be valid standard base64", err)
		}
		write.Data = append([]byte{}, decoded...)
	} else {
		write.Content = *request.Content
	}
	result, err := service.Write(ctx, write)
	if err != nil {
		return tool.Result{}, err
	}
	structured := mutationFromWrite(result)
	content := fmt.Sprintf("%s %s (%d bytes, sha256 %s)", result.Status, result.Path, result.Bytes, result.NewSHA256)
	return tool.Result{Content: content, Structured: structured, Detail: result.Status + " " + result.Path}, nil
}
