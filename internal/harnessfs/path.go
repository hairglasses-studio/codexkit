package harnessfs

import (
	"path"
	"strings"
	"unicode/utf8"
)

const maxSymlinkTargetLength = 4096

const (
	maxPortablePathLength = 4096
	maxPortableNameLength = 255
)

// ValidatePortableRelativePath accepts slash-separated, portable relative
// paths. Empty, dot, parent, absolute, trailing-slash, backslash, control-byte,
// and overlong forms are rejected before they can reach a platform syscall.
func ValidatePortableRelativePath(value string) error {
	if value == "" || len(value) > maxPortablePathLength || !utf8.ValidString(value) {
		return opError("validate-path", value, ErrInvalidPath)
	}
	if strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.Contains(value, "\\") {
		return opError("validate-path", value, ErrInvalidPath)
	}
	for _, segment := range strings.Split(value, "/") {
		if err := validatePortableLeaf(segment); err != nil {
			return opError("validate-path", value, ErrInvalidPath)
		}
	}
	return nil
}

func validatePortableLeaf(value string) error {
	if value == "" || value == "." || value == ".." || len(value) > maxPortableNameLength {
		return ErrInvalidPath
	}
	for _, r := range value {
		if r == 0 || r < 0x20 || r == 0x7f || r == '\\' || r == '/' {
			return ErrInvalidPath
		}
	}
	return nil
}

func splitPortablePath(value string) (parent, leaf string, err error) {
	if err := ValidatePortableRelativePath(value); err != nil {
		return "", "", err
	}
	index := strings.LastIndexByte(value, '/')
	if index < 0 {
		return ".", value, nil
	}
	return value[:index], value[index+1:], nil
}

// ValidateRelativeSymlinkTarget validates a normalized relative symlink target
// against its final destination path. Parent segments are allowed only when
// the resolved target remains beneath the anchored Root.
func ValidateRelativeSymlinkTarget(destinationPath, target string) error {
	if err := ValidatePortableRelativePath(destinationPath); err != nil {
		return err
	}
	if target == "" || len(target) > maxSymlinkTargetLength || !utf8.ValidString(target) ||
		strings.HasPrefix(target, "/") || strings.Contains(target, "\\") || strings.ContainsRune(target, 0) ||
		path.Clean(target) != target || strings.HasSuffix(target, "/") {
		return opError("validate-symlink-target", destinationPath, ErrInvalidPath)
	}
	for _, r := range target {
		if r < 0x20 || r == 0x7f {
			return opError("validate-symlink-target", destinationPath, ErrInvalidPath)
		}
	}
	for _, segment := range strings.Split(target, "/") {
		if segment == "" || segment == "." || len(segment) > maxPortableNameLength {
			return opError("validate-symlink-target", destinationPath, ErrInvalidPath)
		}
	}
	resolved := path.Clean(path.Join(path.Dir(destinationPath), target))
	if resolved == "." || resolved == ".." || strings.HasPrefix(resolved, "../") || strings.HasPrefix(resolved, "/") || resolved == destinationPath {
		return opError("validate-symlink-target", destinationPath, ErrInvalidPath)
	}
	return nil
}

func resolvedSymlinkTarget(destinationPath, target string) string {
	return path.Clean(path.Join(path.Dir(destinationPath), target))
}
