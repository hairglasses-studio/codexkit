package harnessmanifest

import (
	"fmt"
	"path"
	"strings"
)

func validateRelativePath(value string) error {
	if value == "" {
		return fmt.Errorf("path is required")
	}
	if strings.ContainsAny(value, "\\\x00") {
		return fmt.Errorf("path must use portable slash-separated syntax")
	}
	if strings.HasPrefix(value, "/") || path.IsAbs(value) {
		return fmt.Errorf("path must be repository-relative")
	}
	if path.Clean(value) != value || value == "." {
		return fmt.Errorf("path must be normalized")
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("path may not contain empty, dot, or parent segments")
		}
	}
	return nil
}

func validateSymlinkTarget(destination, target string) error {
	if target == "" {
		return fmt.Errorf("symlink target is required")
	}
	if strings.ContainsAny(target, "\\\x00") || strings.HasPrefix(target, "/") || path.IsAbs(target) {
		return fmt.Errorf("symlink target must be a portable relative path")
	}
	if path.Clean(target) != target || target == "." {
		return fmt.Errorf("symlink target must be normalized")
	}
	resolved := path.Clean(path.Join(path.Dir(destination), target))
	if resolved == ".." || strings.HasPrefix(resolved, "../") || strings.HasPrefix(resolved, "/") {
		return fmt.Errorf("symlink target escapes the repository")
	}
	return nil
}

func pathOverlaps(left, right string) bool {
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

func validateOmissionPattern(value string) error {
	if value == "" {
		return fmt.Errorf("path is required")
	}
	if strings.ContainsAny(value, "\\\x00") || strings.HasPrefix(value, "/") || path.IsAbs(value) {
		return fmt.Errorf("pattern must be a portable repository-relative path")
	}
	segments := strings.Split(value, "/")
	for index, segment := range segments {
		switch segment {
		case "", ".", "..":
			return fmt.Errorf("pattern may not contain empty, dot, or parent segments")
		case "*":
			continue
		case "**":
			if index != len(segments)-1 {
				return fmt.Errorf("recursive wildcard must be the terminal segment")
			}
		default:
			if strings.ContainsAny(segment, "*?[]{}") {
				return fmt.Errorf("wildcards must occupy an entire segment")
			}
		}
	}
	return nil
}

func omissionMatches(omission Omission, candidate string) bool {
	if omission.Kind == OmissionExact {
		return omission.Path == candidate
	}
	pattern := strings.Split(omission.Path, "/")
	value := strings.Split(candidate, "/")
	for index, segment := range pattern {
		if segment == "**" {
			return len(value) >= index
		}
		if index >= len(value) || (segment != "*" && segment != value[index]) {
			return false
		}
	}
	return len(pattern) == len(value)
}

func omissionPatternsOverlap(left, right Omission) bool {
	if left.Kind == OmissionExact {
		return omissionMatches(right, left.Path)
	}
	if right.Kind == OmissionExact {
		return omissionMatches(left, right.Path)
	}
	leftSegments := strings.Split(left.Path, "/")
	rightSegments := strings.Split(right.Path, "/")
	leftRecursive := leftSegments[len(leftSegments)-1] == "**"
	rightRecursive := rightSegments[len(rightSegments)-1] == "**"
	leftPrefix := leftSegments
	rightPrefix := rightSegments
	if leftRecursive {
		leftPrefix = leftSegments[:len(leftSegments)-1]
	}
	if rightRecursive {
		rightPrefix = rightSegments[:len(rightSegments)-1]
	}
	common := min(len(leftPrefix), len(rightPrefix))
	for index := 0; index < common; index++ {
		if leftPrefix[index] != "*" && rightPrefix[index] != "*" && leftPrefix[index] != rightPrefix[index] {
			return false
		}
	}
	if leftRecursive && rightRecursive {
		return true
	}
	if leftRecursive {
		return len(rightPrefix) >= len(leftPrefix)
	}
	if rightRecursive {
		return len(leftPrefix) >= len(rightPrefix)
	}
	return len(leftPrefix) == len(rightPrefix)
}
