// Package safefs provides filesystem operations confined to an opened directory root.
package safefs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Root wraps os.Root and keeps every relative operation beneath the opened directory.
type Root struct {
	path string
	root *os.Root
}

// Open opens path as a filesystem boundary for subsequent relative operations.
func Open(path string) (*Root, error) {
	clean, err := cleanRootPath(path)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(clean)
	if err != nil {
		return nil, fmt.Errorf("open filesystem root %q: %w", clean, err)
	}
	return &Root{path: clean, root: root}, nil
}

// Ensure creates path when necessary and opens it as a filesystem boundary.
func Ensure(path string, perm fs.FileMode) (*Root, error) {
	clean, err := cleanRootPath(path)
	if err != nil {
		return nil, err
	}
	root, err := Open(clean)
	if err == nil {
		return root, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	ancestor := filepath.Dir(clean)
	missing := []string{filepath.Base(clean)}
	for {
		ancestorRoot, openErr := os.OpenRoot(ancestor)
		if openErr == nil {
			relative := filepath.Join(missing...)
			if mkdirErr := ancestorRoot.MkdirAll(relative, perm); mkdirErr != nil {
				_ = ancestorRoot.Close()
				return nil, fmt.Errorf("create filesystem root %q: %w", clean, mkdirErr)
			}
			createdRoot, openRootErr := ancestorRoot.OpenRoot(relative)
			closeErr := ancestorRoot.Close()
			if openRootErr != nil {
				return nil, fmt.Errorf("open created filesystem root %q: %w", clean, openRootErr)
			}
			if closeErr != nil {
				_ = createdRoot.Close()
				return nil, fmt.Errorf("close parent filesystem root %q: %w", ancestor, closeErr)
			}
			return &Root{path: clean, root: createdRoot}, nil
		}
		if !errors.Is(openErr, os.ErrNotExist) {
			return nil, fmt.Errorf("open ancestor filesystem root %q: %w", ancestor, openErr)
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return nil, fmt.Errorf("find existing ancestor for filesystem root %q: %w", clean, openErr)
		}
		missing = append([]string{filepath.Base(ancestor)}, missing...)
		ancestor = parent
	}
}

// Close releases the opened root.
func (r *Root) Close() error {
	if r == nil || r.root == nil {
		return nil
	}
	return r.root.Close()
}

// Path returns the cleaned path used to open the root.
func (r *Root) Path() string {
	return r.path
}

// FullPath returns a display path for a validated relative name.
// Callers must use Root methods, rather than the returned path, for filesystem access.
func (r *Root) FullPath(name string) (string, error) {
	clean, err := cleanRelative(name, true)
	if err != nil {
		return "", err
	}
	if clean == "." {
		return r.path, nil
	}
	return filepath.Join(r.path, clean), nil
}

// ReadFile reads a file without allowing symlink traversal outside the root.
func (r *Root) ReadFile(name string) ([]byte, error) {
	clean, err := cleanRelative(name, false)
	if err != nil {
		return nil, err
	}
	return r.root.ReadFile(clean)
}

// WriteFile writes a file without allowing symlink traversal outside the
// root, creating missing parent directories beneath it.
func (r *Root) WriteFile(name string, data []byte, perm fs.FileMode) error {
	clean, err := cleanRelative(name, false)
	if err != nil {
		return err
	}
	if err := r.ensureParent(clean); err != nil {
		return err
	}
	return r.root.WriteFile(clean, data, perm)
}

// WriteFileAtomic writes data through a temp file in the same directory
// and renames it over name, so a crash mid-write cannot truncate the file
// in place of the original. The existing file's mode is preserved.
func (r *Root) WriteFileAtomic(name string, data []byte, perm fs.FileMode) error {
	clean, err := cleanRelative(name, false)
	if err != nil {
		return err
	}
	if err := r.ensureParent(clean); err != nil {
		return err
	}
	if info, statErr := r.root.Stat(clean); statErr == nil {
		perm = info.Mode().Perm()
	}
	dir := filepath.Dir(clean)
	base := filepath.Base(clean)
	var file *os.File
	var tmpName string
	for range 5 {
		var noise [6]byte
		if _, err := rand.Read(noise[:]); err != nil {
			return err
		}
		tmpName = filepath.Join(dir, "."+base+"."+hex.EncodeToString(noise[:])+".tmp")
		file, err = r.root.OpenFile(tmpName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	if file == nil {
		return errors.New("atomic write: temp name allocation failed")
	}
	writeErr := func() error {
		if _, err := file.Write(data); err != nil {
			return err
		}
		return file.Sync()
	}()
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = r.root.Remove(tmpName)
		if writeErr != nil {
			return writeErr
		}
		return closeErr
	}
	if err := r.root.Rename(tmpName, clean); err != nil {
		_ = r.root.Remove(tmpName)
		return err
	}
	return nil
}

// WriteFileAtomicPath is WriteFileAtomic for absolute paths outside any Root.
func WriteFileAtomicPath(path string, data []byte, perm fs.FileMode) error {
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	writeErr := func() error {
		if _, err := tmp.Write(data); err != nil {
			return err
		}
		return tmp.Sync()
	}()
	closeErr := tmp.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(tmpName)
		if writeErr != nil {
			return writeErr
		}
		return closeErr
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// CreateExclusive creates a file with data only if it does not exist yet,
// without allowing symlink traversal outside the root. It reports an error
// wrapping os.ErrExist when the name is already taken.
func (r *Root) CreateExclusive(name string, data []byte, perm fs.FileMode) error {
	clean, err := cleanRelative(name, false)
	if err != nil {
		return err
	}
	if err := r.ensureParent(clean); err != nil {
		return err
	}
	file, err := r.root.OpenFile(clean, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// ensureParent creates the directory a write is about to land in when it
// does not exist yet. MkdirAll beneath os.Root keeps confinement: a symlink
// in the way that resolves outside the root is refused, not followed.
func (r *Root) ensureParent(clean string) error {
	dir := filepath.Dir(clean)
	if dir == "." {
		return nil
	}
	if _, err := r.root.Stat(dir); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := r.root.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create parent directory %q beneath root: %w", dir, err)
	}
	return nil
}

// Stat returns file information without allowing traversal outside the root.
func (r *Root) Stat(name string) (fs.FileInfo, error) {
	clean, err := cleanRelative(name, true)
	if err != nil {
		return nil, err
	}
	return r.root.Stat(clean)
}

// Lstat returns information about a path without following its final symlink.
func (r *Root) Lstat(name string) (fs.FileInfo, error) {
	clean, err := cleanRelative(name, true)
	if err != nil {
		return nil, err
	}
	return r.root.Lstat(clean)
}

// MkdirAll creates a directory tree without allowing traversal outside the root.
func (r *Root) MkdirAll(name string, perm fs.FileMode) error {
	clean, err := cleanRelative(name, false)
	if err != nil {
		return err
	}
	return r.root.MkdirAll(clean, perm)
}

// ReadDir reads and returns all entries in a directory beneath the root.
func (r *Root) ReadDir(name string) ([]fs.DirEntry, error) {
	clean, err := cleanRelative(name, true)
	if err != nil {
		return nil, err
	}
	dir, err := r.root.Open(clean)
	if err != nil {
		return nil, err
	}
	entries, readErr := dir.ReadDir(-1)
	closeErr := dir.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return entries, nil
}

// Remove removes a file or empty directory beneath the root.
func (r *Root) Remove(name string) error {
	clean, err := cleanRelative(name, false)
	if err != nil {
		return err
	}
	return r.root.Remove(clean)
}

// Rename renames a path while keeping both endpoints beneath the root.
func (r *Root) Rename(oldName string, newName string) error {
	cleanOld, err := cleanRelative(oldName, false)
	if err != nil {
		return err
	}
	cleanNew, err := cleanRelative(newName, false)
	if err != nil {
		return err
	}
	return r.root.Rename(cleanOld, cleanNew)
}

// FS exposes a read-only fs.FS view rooted at the same boundary.
func (r *Root) FS() fs.FS {
	return r.root.FS()
}

func cleanRootPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("filesystem root path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve filesystem root %q: %w", path, err)
	}
	return filepath.Clean(absolute), nil
}

func cleanRelative(name string, allowRoot bool) (string, error) {
	if strings.TrimSpace(name) == "" {
		if allowRoot {
			return ".", nil
		}
		return "", errors.New("relative path is required")
	}
	if filepath.IsAbs(name) || filepath.VolumeName(name) != "" {
		return "", fmt.Errorf("path must be relative to filesystem root: %q", name)
	}
	clean := filepath.Clean(name)
	if clean == "." {
		if allowRoot {
			return clean, nil
		}
		return "", errors.New("relative path must name an entry")
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes filesystem root: %q", name)
	}
	return clean, nil
}
