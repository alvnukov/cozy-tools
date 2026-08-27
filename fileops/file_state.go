package fileops

import (
	"errors"
	"os"
)

// FileState is one file's pre-mutation content, or its absence, captured so a
// caller can undo a partially applied series of edits.
type FileState struct {
	Exists  bool
	Content []byte
}

// CaptureFileStateInRepo reads the current state of a repo-relative file.
func CaptureFileStateInRepo(repoPath string, filePath string) (FileState, error) {
	scoped, err := openScopedFile(repoPath, filePath, false)
	if err != nil {
		return FileState{}, err
	}
	defer scoped.close()
	data, err := scoped.root.ReadFile(scoped.name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return FileState{Exists: false}, nil
		}
		return FileState{}, err
	}
	return FileState{Exists: true, Content: data}, nil
}

// RestoreFileStateInRepo puts a repo-relative file back to a captured state:
// content rewritten, or the file removed when it did not exist. Removing an
// already absent file succeeds, so a restore can run twice.
func RestoreFileStateInRepo(repoPath string, filePath string, state FileState) error {
	scoped, err := openScopedFile(repoPath, filePath, false)
	if err != nil {
		return err
	}
	defer scoped.close()
	if !state.Exists {
		if err := scoped.root.Remove(scoped.name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return scoped.root.WriteFileAtomic(scoped.name, state.Content, 0o600)
}
