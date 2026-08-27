package filesystem

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

// Write atomically creates or replaces one regular file.
func (s *Service) Write(ctx context.Context, request WriteRequest) (WriteResult, error) {
	var result WriteResult
	done, err := s.begin(ctx, "write")
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
	var content []byte
	if request.Data != nil {
		content = append([]byte(nil), request.Data...)
	} else {
		content = []byte(request.Content)
	}
	if int64(len(content)) > s.limits.WholeFileBytes {
		return result, newError(CodeLimit, "write", name, "content exceeds whole-file cap", nil)
	}
	mode := request.Mode
	if mode == "" {
		mode = WriteOverwrite
	}
	if request.CreateOnly {
		mode = WriteCreateOnly
	}
	if mode != WriteOverwrite && mode != WriteCreateOnly {
		return result, newError(CodeInvalidInput, "write", name, "unknown write mode", nil)
	}
	if err := validateExpectedHash(request.ExpectedSHA256); err != nil {
		return result, err
	}
	if request.Permissions != nil && *request.Permissions > 0o7777 {
		return result, newError(CodeInvalidInput, "write", name, "permissions exceed 07777", nil)
	}

	existing, info, exists, err := s.readExisting(ctx, name)
	if err != nil {
		return result, err
	}
	oldHash := ""
	if exists {
		oldHash = hashBytes(existing)
		if err := matchExpected(name, oldHash, request.ExpectedSHA256); err != nil {
			return result, err
		}
		sameMode := request.Permissions == nil || modeToUnix(info.Mode()) == *request.Permissions
		if bytes.Equal(existing, content) && sameMode {
			return WriteResult{Path: name, OldSHA256: oldHash, NewSHA256: oldHash, Changed: false, Status: "unchanged", Bytes: int64(len(content)), Mode: modeToUnix(info.Mode())}, nil
		}
		if mode == WriteCreateOnly {
			return result, newError(CodeConflict, "write", name, "create-only target already exists", os.ErrExist)
		}
	} else if request.ExpectedSHA256 != "" {
		return result, newError(CodeConflict, "write", name, "expected hash supplied for missing file", os.ErrNotExist)
	}
	if err := checkContext(ctx); err != nil {
		return result, err
	}

	permissions := os.FileMode(0o666)
	if exists {
		permissions = preservedMode(info.Mode())
	}
	if request.Permissions != nil {
		permissions = unixMode(*request.Permissions)
	}
	exactMode := exists || request.Permissions != nil
	if err := s.atomicWrite(ctx, name, content, permissions, exactMode, mode == WriteCreateOnly); err != nil {
		return result, err
	}
	writtenInfo, err := s.root.Lstat(name)
	if err != nil {
		return result, rootError("stat", name, err)
	}
	newHash := hashBytes(content)
	status := "created"
	if exists {
		status = "updated"
	}
	return WriteResult{Path: name, OldSHA256: oldHash, NewSHA256: newHash, Changed: true, Status: status, Bytes: int64(len(content)), Mode: modeToUnix(writtenInfo.Mode())}, nil
}

func (s *Service) readExisting(ctx context.Context, name string) ([]byte, os.FileInfo, bool, error) {
	info, err := s.root.Lstat(name)
	if err != nil {
		if errorIsNotExist(err) {
			return nil, nil, false, nil
		}
		return nil, nil, false, rootError("stat", name, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, nil, false, newError(CodePermission, "write", name, "refusing to replace a symlink", os.ErrPermission)
	}
	if !info.Mode().IsRegular() {
		return nil, nil, false, newError(CodeInvalidInput, "write", name, "target is not a regular file", nil)
	}
	if info.Size() > s.limits.WholeFileBytes {
		return nil, nil, false, newError(CodeLimit, "write", name, "existing file exceeds whole-file cap", nil)
	}
	file, err := s.root.Open(name)
	if err != nil {
		return nil, nil, false, rootError("read", name, err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, s.limits.WholeFileBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, nil, false, rootError("read", name, readErr)
	}
	if closeErr != nil {
		return nil, nil, false, rootError("read", name, closeErr)
	}
	if int64(len(data)) > s.limits.WholeFileBytes {
		return nil, nil, false, newError(CodeLimit, "read", name, "file grew beyond whole-file cap", nil)
	}
	if err := checkContext(ctx); err != nil {
		return nil, nil, false, err
	}
	return data, info, true, nil
}

func validateExpectedHash(expected string) error {
	if expected == "" {
		return nil
	}
	if len(expected) < 4 || len(expected) > 64 {
		return newError(CodeInvalidInput, "hash", expected, "expected SHA-256 must be a 4-64 character prefix", nil)
	}
	if _, err := hex.DecodeString(expected + strings.Repeat("0", len(expected)%2)); err != nil {
		return newError(CodeInvalidInput, "hash", expected, "expected SHA-256 must be hexadecimal", err)
	}
	return nil
}

func matchExpected(name, actual, expected string) error {
	if expected == "" {
		return nil
	}
	if !strings.HasPrefix(actual, strings.ToLower(expected)) {
		return newError(CodeConflict, "hash", name, fmt.Sprintf("stale snapshot: expected %s, actual %s", expected, actual), nil)
	}
	return nil
}

func (s *Service) atomicWrite(ctx context.Context, name string, content []byte, permissions os.FileMode, exactMode, createOnly bool) error {
	directory := path.Dir(name)
	if err := s.root.MkdirAll(directory, 0o755); err != nil {
		return rootError("mkdir", directory, err)
	}
	tempName, err := randomTempName(directory)
	if err != nil {
		return newError(CodeInternal, "write", name, "generate temporary name", err)
	}
	createMode := os.FileMode(0o600)
	if !exactMode {
		createMode = permissions
	}
	temp, err := s.root.OpenFile(tempName, os.O_RDWR|os.O_CREATE|os.O_EXCL, createMode)
	if err != nil {
		return rootError("write", tempName, err)
	}
	keepTemp := true
	defer func() {
		_ = temp.Close()
		if keepTemp {
			_ = s.root.Remove(tempName)
		}
	}()
	finalMode := permissions
	if !exactMode {
		info, err := temp.Stat()
		if err != nil {
			return rootError("stat", tempName, err)
		}
		finalMode = preservedMode(info.Mode())
		if err := temp.Chmod(0o600); err != nil {
			return rootError("chmod", tempName, err)
		}
	}
	if n, err := temp.Write(content); err != nil {
		return rootError("write", tempName, err)
	} else if n != len(content) {
		return rootError("write", tempName, io.ErrShortWrite)
	}
	if err := temp.Chmod(finalMode); err != nil {
		return rootError("chmod", tempName, err)
	}
	if err := temp.Sync(); err != nil {
		return rootError("sync", tempName, err)
	}
	if err := temp.Close(); err != nil {
		return rootError("close", tempName, err)
	}
	if err := checkContext(ctx); err != nil {
		return err
	}
	if createOnly {
		if err := s.root.Link(tempName, name); err != nil {
			return rootError("link", name, err)
		}
		if err := s.root.Remove(tempName); err != nil {
			return rootError("remove", tempName, err)
		}
		keepTemp = false
	} else {
		if err := s.root.Rename(tempName, name); err != nil {
			return rootError("rename", name, err)
		}
		keepTemp = false
	}
	if err := s.syncDirectory(directory); err != nil {
		return err
	}
	return nil
}

func randomTempName(directory string) (string, error) {
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return path.Join(directory, ".cozy-tmp-"+hex.EncodeToString(random[:])), nil
}

func preservedMode(mode os.FileMode) os.FileMode {
	return mode & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
}

func unixMode(value uint32) os.FileMode {
	mode := os.FileMode(value & 0o777)
	if value&0o4000 != 0 {
		mode |= os.ModeSetuid
	}
	if value&0o2000 != 0 {
		mode |= os.ModeSetgid
	}
	if value&0o1000 != 0 {
		mode |= os.ModeSticky
	}
	return mode
}

func modeToUnix(mode os.FileMode) uint32 {
	value := uint32(mode.Perm())
	if mode&os.ModeSetuid != 0 {
		value |= 0o4000
	}
	if mode&os.ModeSetgid != 0 {
		value |= 0o2000
	}
	if mode&os.ModeSticky != 0 {
		value |= 0o1000
	}
	return value
}

func (s *Service) syncDirectory(directory string) error {
	file, err := s.root.Open(directory)
	if err != nil {
		return rootError("open", directory, err)
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if syncErr != nil {
		return rootError("sync", directory, syncErr)
	}
	if closeErr != nil {
		return rootError("close", directory, closeErr)
	}
	return nil
}
