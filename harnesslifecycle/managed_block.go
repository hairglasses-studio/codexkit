package harnesslifecycle

import (
	"fmt"
	"strings"
)

const (
	defaultManagedBlockOwner = "CODEX-HARNESS-STAGING"
)

func managedBlockMarkers(owner string) (string, string) {
	if strings.TrimSpace(owner) == "" {
		owner = defaultManagedBlockOwner
	}
	begin := fmt.Sprintf("# BEGIN %s MANAGED CONFIG", owner)
	end := fmt.Sprintf("# END %s MANAGED CONFIG", owner)
	return begin, end
}

// FormatManagedBlock wraps rendered configuration content with standard fence
// markers naming the given owner.
func FormatManagedBlock(rendered, owner string) string {
	begin, end := managedBlockMarkers(owner)
	trimmed := strings.TrimRight(rendered, "\r\n")
	if trimmed == "" {
		return begin + "\n" + end + "\n"
	}
	return begin + "\n" + trimmed + "\n" + end + "\n"
}

// ExtractManagedBlock locates the fence-delimited managed block in content for
// the specified owner. It returns the interior block content, any prefix before
// the begin marker, any suffix after the end marker, and whether the block was
// found.
func ExtractManagedBlock(content, owner string) (block string, prefix string, suffix string, found bool) {
	begin, end := managedBlockMarkers(owner)
	lines := strings.Split(content, "\n")
	beginIndex := -1
	endIndex := -1

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if beginIndex < 0 {
			if trimmed == begin || strings.EqualFold(trimmed, begin) {
				beginIndex = i
			}
		} else if endIndex < 0 {
			if trimmed == end || strings.EqualFold(trimmed, end) {
				endIndex = i
				break
			}
		}
	}

	if beginIndex < 0 || endIndex < 0 || endIndex < beginIndex {
		return "", content, "", false
	}

	prefixLines := lines[:beginIndex]
	blockLines := lines[beginIndex+1 : endIndex]
	suffixLines := lines[endIndex+1:]

	prefix = strings.Join(prefixLines, "\n")
	if len(prefixLines) > 0 && len(content) > 0 && !strings.HasSuffix(prefix, "\n") && beginIndex > 0 {
		prefix += "\n"
	}
	block = strings.Join(blockLines, "\n")
	if len(blockLines) > 0 {
		block += "\n"
	}
	suffix = strings.Join(suffixLines, "\n")

	return block, prefix, suffix, true
}

// SpliceManagedBlock merges newBlock into existing content under fence markers
// for the given owner, preserving unmanaged prefix and suffix content.
func SpliceManagedBlock(existing, newBlock, owner string) (string, error) {
	formatted := FormatManagedBlock(newBlock, owner)
	_, prefix, suffix, found := ExtractManagedBlock(existing, owner)
	if found {
		var result strings.Builder
		if prefix != "" {
			result.WriteString(prefix)
			if !strings.HasSuffix(prefix, "\n") {
				result.WriteString("\n")
			}
		}
		result.WriteString(formatted)
		if suffix != "" {
			trimmedSuffix := strings.TrimLeft(suffix, "\r\n")
			if trimmedSuffix != "" {
				result.WriteString(trimmedSuffix)
			}
		}
		return result.String(), nil
	}

	trimmedExisting := strings.TrimRight(existing, "\r\n")
	if trimmedExisting == "" {
		return formatted, nil
	}
	return trimmedExisting + "\n\n" + formatted, nil
}

// RemoveManagedBlock strips the fence-delimited managed block from existing
// content, returning the remaining content and whether the block was removed.
func RemoveManagedBlock(existing, owner string) (string, bool) {
	_, prefix, suffix, found := ExtractManagedBlock(existing, owner)
	if !found {
		return existing, false
	}

	var result strings.Builder
	trimmedPrefix := strings.TrimRight(prefix, "\r\n")
	trimmedSuffix := strings.TrimLeft(suffix, "\r\n")

	if trimmedPrefix != "" {
		result.WriteString(trimmedPrefix)
	}
	if trimmedSuffix != "" {
		if result.Len() > 0 {
			result.WriteString("\n\n")
		}
		result.WriteString(trimmedSuffix)
	}
	if result.Len() > 0 && !strings.HasSuffix(result.String(), "\n") {
		result.WriteString("\n")
	}
	return result.String(), true
}
