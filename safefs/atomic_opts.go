package safefs

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// WriteOptions selects atomic-replacement semantics. It is the single set
// of knobs every atomic write in the file family goes through.
type WriteOptions struct {
	// ExactMode installs perm exactly. Without it, an existing target's mode
	// is preserved, and a new file's mode is perm as filtered by the
	// process umask, with setuid/setgid/sticky bits carried over from the
	// created temporary file.
	ExactMode bool
	// CreateOnly installs the file with link-if-absent semantics: it fails
	// with os.ErrExist when the target already exists, instead of replacing
	// it.
	CreateOnly bool
}

// WriteFileAtomicOpts atomically installs data at name under the options:
// the content is written to an exclusive temporary file in the target's
// directory, flushed, mode-adjusted, and then linked into place (CreateOnly)
// or renamed over the target. On every failure path the temporary file is
// removed and the target is untouched. Parent directories are created when
// missing.
//
// This is the authoritative atomic replacement of the file family:
// filesystem.Service and fileops delegate here rather than reimplementing
// the temp/rename dance.
func (r *Root) WriteFileAtomicOpts(name string, data []byte, perm fs.FileMode, opts WriteOptions) error {
	clean, err := cleanRelative(name, false)
	if err != nil {
		return err
	}
	if err := r.ensureParent(clean); err != nil {
		return err
	}
	directory := filepath.Dir(clean)
	tempName, err := randomCozyTempName(directory)
	if err != nil {
		return err
	}
	createMode := fs.FileMode(0o600)
	if !opts.ExactMode {
		createMode = perm
	}
	temp, err := r.root.OpenFile(tempName, os.O_RDWR|os.O_CREATE|os.O_EXCL, createMode)
	if err != nil {
		return err
	}
	keepTemp := true
	defer func() {
		_ = temp.Close()
		if keepTemp {
			_ = r.root.Remove(tempName)
		}
	}()
	finalMode := perm
	if !opts.ExactMode {
		if info, statErr := r.root.Lstat(clean); statErr == nil {
			finalMode = preservedMode(info.Mode())
		}
	}
	if n, writeErr := temp.Write(data); writeErr != nil {
		return writeErr
	} else if n != len(data) {
		return io.ErrShortWrite
	}
	if err := temp.Chmod(finalMode); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if opts.CreateOnly {
		if err := r.root.Link(tempName, clean); err != nil {
			return err
		}
		if err := r.root.Remove(tempName); err != nil {
			return err
		}
		keepTemp = false
	} else {
		if err := r.root.Rename(tempName, clean); err != nil {
			return err
		}
		keepTemp = false
	}
	return r.syncDir(directory)
}

// Link creates newName as a hard link to oldName, both root-relative.
func (r *Root) Link(oldName string, newName string) error {
	oldClean, err := cleanRelative(oldName, false)
	if err != nil {
		return err
	}
	newClean, err := cleanRelative(newName, false)
	if err != nil {
		return err
	}
	return r.root.Link(oldClean, newClean)
}

// syncDir best-effort flushes a directory entry so a completed rename
// survives a crash.
func (r *Root) syncDir(directory string) error {
	file, err := r.root.Open(directory)
	if err != nil {
		return err
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

// randomCozyTempName reserves a fresh temporary name in directory.
func randomCozyTempName(directory string) (string, error) {
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return filepath.Join(directory, ".cozy-tmp-"+hex.EncodeToString(random[:])), nil
}

// preservedMode keeps the permission and special bits of mode.
func preservedMode(mode fs.FileMode) fs.FileMode {
	return mode.Perm() | (mode & (fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky))
}
