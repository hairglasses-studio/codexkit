package researchgateway

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const maxMetadataBytes = int64(4 << 20)

var rootIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

func resolveHarnessRoot(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("%w: harness_root is required", ErrInvalidConfig)
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("%w: resolve harness_root: %v", ErrInvalidConfig, err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("%w: resolve harness_root: %v", ErrInvalidConfig, err)
	}
	info, err := os.Stat(real)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("%w: harness_root is not a directory", ErrInvalidConfig)
	}
	for _, marker := range []string{"AGENTS.md", filepath.Join("codexkit", "go.mod")} {
		markerInfo, markerErr := os.Stat(filepath.Join(real, marker))
		if markerErr != nil || !markerInfo.Mode().IsRegular() {
			return "", fmt.Errorf("%w: harness_root lacks %s", ErrInvalidConfig, filepath.ToSlash(marker))
		}
	}
	return filepath.Clean(real), nil
}

func resolveExisting(root, raw, fallback string, wantDir bool) (string, error) {
	if raw == "" {
		raw = fallback
	}
	candidate, err := lexicalContained(root, raw)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", err
	}
	if !pathWithin(root, real) {
		return "", fmt.Errorf("%w: %s", ErrPathEscape, raw)
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if wantDir && !info.IsDir() {
		return "", fmt.Errorf("%w: %s is not a directory", ErrInvalidConfig, raw)
	}
	if !wantDir && !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: %s is not a regular file", ErrInvalidConfig, raw)
	}
	return filepath.Clean(real), nil
}

func resolveOptionalFile(root, raw, fallback string) (string, bool, error) {
	if raw == "" {
		raw = fallback
	}
	candidate, err := lexicalContained(root, raw)
	if err != nil {
		return "", false, err
	}
	real, err := filepath.EvalSymlinks(candidate)
	if err == nil {
		if !pathWithin(root, real) {
			return "", false, fmt.Errorf("%w: %s", ErrPathEscape, raw)
		}
		info, statErr := os.Stat(real)
		if statErr != nil {
			return "", false, statErr
		}
		if !info.Mode().IsRegular() {
			return "", false, fmt.Errorf("%w: %s is not a regular file", ErrInvalidConfig, raw)
		}
		return filepath.Clean(real), true, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", false, err
	}
	parent, parentErr := filepath.EvalSymlinks(filepath.Dir(candidate))
	if parentErr != nil {
		return "", false, parentErr
	}
	if !pathWithin(root, parent) {
		return "", false, fmt.Errorf("%w: %s", ErrPathEscape, raw)
	}
	return filepath.Join(parent, filepath.Base(candidate)), false, nil
}

func lexicalContained(root, raw string) (string, error) {
	if strings.TrimSpace(raw) == "" || strings.ContainsRune(raw, '\x00') {
		return "", fmt.Errorf("%w: empty or invalid path", ErrInvalidConfig)
	}
	candidate := raw
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if !pathWithin(root, abs) {
		return "", fmt.Errorf("%w: %s", ErrPathEscape, raw)
	}
	return abs, nil
}

func pathWithin(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func harnessRelative(root, path string) (string, error) {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || !pathWithin(root, path) {
		return "", fmt.Errorf("%w: cannot make harness-relative path", ErrPathEscape)
	}
	return filepath.ToSlash(relative), nil
}

func readBoundedFile(path string, max int64) ([]byte, fs.FileInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("not a regular file: %s", path)
	}
	if info.Size() > max {
		return nil, nil, fmt.Errorf("%w: %s is %d bytes (max %d)", ErrLimitExceeded, path, info.Size(), max)
	}
	data, err := io.ReadAll(io.LimitReader(file, max+1))
	if err != nil {
		return nil, nil, err
	}
	if int64(len(data)) > max {
		return nil, nil, fmt.Errorf("%w: %s exceeds %d bytes", ErrLimitExceeded, path, max)
	}
	return data, info, nil
}

func rootsOverlap(left, right string) bool {
	return pathWithin(left, right) || pathWithin(right, left)
}
