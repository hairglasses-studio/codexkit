// Package toml provides a minimal TOML writer for generating .codex/config.toml
// sections. It preserves codexkit's zero-dependency constraint.
package toml

import (
	"fmt"
	"sort"
	"strings"
)

// Section represents a TOML section with key-value pairs.
type Section struct {
	Name   string
	Values map[string]any
}

// WriteSections serializes sections to TOML format.
func WriteSections(sections []Section) string {
	var b strings.Builder
	for i, s := range sections {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "[%s]\n", s.Name)
		keys := sortedKeys(s.Values)
		for _, k := range keys {
			writeValue(&b, k, s.Values[k])
		}
	}
	return b.String()
}

// AppendSection generates a single TOML section block that can be
// appended to an existing config file.
func AppendSection(name string, values map[string]any) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n[%s]\n", name)
	keys := sortedKeys(values)
	for _, k := range keys {
		writeValue(&b, k, values[k])
	}
	return b.String()
}

func writeValue(b *strings.Builder, key string, value any) {
	switch v := value.(type) {
	case string:
		fmt.Fprintf(b, "%s = %q\n", key, v)
	case bool:
		fmt.Fprintf(b, "%s = %v\n", key, v)
	case int:
		fmt.Fprintf(b, "%s = %d\n", key, v)
	case float64:
		fmt.Fprintf(b, "%s = %v\n", key, v)
	case []string:
		fmt.Fprintf(b, "%s = [%s]\n", key, quoteSlice(v))
	default:
		fmt.Fprintf(b, "%s = %q\n", key, fmt.Sprint(v))
	}
}

func quoteSlice(ss []string) string {
	quoted := make([]string, len(ss))
	for i, s := range ss {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(quoted, ", ")
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
