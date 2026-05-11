package unificationaudit

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var shellFunctionDefRe = regexp.MustCompile(`^\s*(?:function\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*(?:\(\s*\))?\s*\{?\s*(?:#.*)?$`)

// ShellFileInventory captures function and caller boundaries for one shell
// file before a migration slice starts.
type ShellFileInventory struct {
	RepoPath    string          `json:"repo_path"`
	Path        string          `json:"path"`
	Lines       int             `json:"lines"`
	Functions   []ShellFunction `json:"functions,omitempty"`
	Entrypoints []ShellFunction `json:"entrypoints,omitempty"`
	SourcedBy   []ShellCaller   `json:"sourced_by,omitempty"`
}

// ShellFunction is one shell function definition in a selected file.
type ShellFunction struct {
	Name    string        `json:"name"`
	Line    int           `json:"line"`
	Callers []ShellCaller `json:"callers,omitempty"`
}

// ShellCaller is one obvious text-level reference to a function or source file.
type ShellCaller struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text,omitempty"`
}

// InventoryShellFile reads one repo-local shell file and maps function
// definitions plus simple repo-wide caller references.
func InventoryShellFile(repoPath, relPath string) (ShellFileInventory, error) {
	if repoPath == "" {
		return ShellFileInventory{}, fmt.Errorf("repo path is required")
	}
	if relPath == "" {
		return ShellFileInventory{}, fmt.Errorf("shell file path is required")
	}
	absRepoPath, err := filepath.Abs(repoPath)
	if err != nil {
		return ShellFileInventory{}, err
	}
	relPath = filepath.ToSlash(filepath.Clean(relPath))
	if strings.HasPrefix(relPath, "../") || filepath.IsAbs(relPath) {
		return ShellFileInventory{}, fmt.Errorf("shell file path must be repo-relative: %s", relPath)
	}
	absPath := filepath.Join(absRepoPath, filepath.FromSlash(relPath))
	data, err := os.ReadFile(absPath)
	if err != nil {
		return ShellFileInventory{}, err
	}
	lines := splitLines(data)
	inventory := ShellFileInventory{
		RepoPath: absRepoPath,
		Path:     relPath,
		Lines:    len(lines),
	}
	inventory.Functions = parseShellFunctions(lines)
	for _, fn := range inventory.Functions {
		if strings.HasPrefix(fn.Name, "cmd_") {
			inventory.Entrypoints = append(inventory.Entrypoints, fn)
		}
	}
	callers, sourcedBy, err := scanShellCallers(absRepoPath, relPath, inventory.Functions)
	if err != nil {
		return ShellFileInventory{}, err
	}
	for i := range inventory.Functions {
		inventory.Functions[i].Callers = callers[inventory.Functions[i].Name]
	}
	inventory.SourcedBy = sourcedBy
	return inventory, nil
}

func parseShellFunctions(lines []string) []ShellFunction {
	var functions []ShellFunction
	for idx, line := range lines {
		name, ok := parseShellFunctionLine(line)
		if !ok {
			continue
		}
		functions = append(functions, ShellFunction{Name: name, Line: idx + 1})
	}
	sort.Slice(functions, func(i, j int) bool {
		if functions[i].Line == functions[j].Line {
			return functions[i].Name < functions[j].Name
		}
		return functions[i].Line < functions[j].Line
	})
	return functions
}

func parseShellFunctionLine(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	matches := shellFunctionDefRe.FindStringSubmatch(line)
	if len(matches) != 2 {
		return "", false
	}
	name := matches[1]
	if strings.ContainsAny(name, "-.") || shellKeywordNames[name] {
		return "", false
	}
	if !strings.Contains(trimmed, "()") && !strings.HasPrefix(trimmed, "function ") {
		return "", false
	}
	return name, true
}

var shellKeywordNames = map[string]bool{
	"case":  true,
	"do":    true,
	"done":  true,
	"elif":  true,
	"else":  true,
	"esac":  true,
	"fi":    true,
	"for":   true,
	"if":    true,
	"then":  true,
	"while": true,
}

func scanShellCallers(repoPath, relPath string, functions []ShellFunction) (map[string][]ShellCaller, []ShellCaller, error) {
	callers := make(map[string][]ShellCaller, len(functions))
	defLines := make(map[string]map[int]bool, len(functions))
	for _, fn := range functions {
		defLines[fn.Name] = map[int]bool{fn.Line: true}
	}
	sourcedBy := []ShellCaller{}
	functionNames := make([]string, 0, len(functions))
	for _, fn := range functions {
		functionNames = append(functionNames, fn.Name)
	}
	sort.Strings(functionNames)

	err := filepath.WalkDir(repoPath, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(repoPath, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if shouldSkipDir(rel, entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() || !likelyShellFile(rel, info.Mode()) {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil || !isShellFile(data, rel) {
			return nil
		}
		lines := splitLines(data)
		for idx, line := range lines {
			lineNumber := idx + 1
			if rel != relPath && lineSourcesShellFile(line, relPath) {
				sourcedBy = appendLimitedCaller(sourcedBy, ShellCaller{Path: rel, Line: lineNumber, Text: strings.TrimSpace(line)}, 16)
			}
			for _, name := range functionNames {
				if rel == relPath && defLines[name][lineNumber] {
					continue
				}
				if !lineReferencesName(line, name) {
					continue
				}
				callers[name] = appendLimitedCaller(callers[name], ShellCaller{Path: rel, Line: lineNumber, Text: strings.TrimSpace(line)}, 12)
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return callers, sourcedBy, nil
}

func lineReferencesName(line, name string) bool {
	if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
		return false
	}
	re := regexp.MustCompile(`(^|[^A-Za-z0-9_])` + regexp.QuoteMeta(name) + `([^A-Za-z0-9_]|$)`)
	return re.MatchString(line)
}

func lineSourcesShellFile(line, relPath string) bool {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "#") {
		return false
	}
	base := filepath.Base(relPath)
	return (strings.HasPrefix(trimmed, "source ") || strings.HasPrefix(trimmed, ". ")) &&
		(strings.Contains(trimmed, relPath) || strings.Contains(trimmed, base))
}

func appendLimitedCaller(callers []ShellCaller, caller ShellCaller, limit int) []ShellCaller {
	for _, existing := range callers {
		if existing.Path == caller.Path && existing.Line == caller.Line {
			return callers
		}
	}
	if len(callers) >= limit {
		return callers
	}
	return append(callers, caller)
}

func splitLines(data []byte) []string {
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	text := string(data)
	if text == "" {
		return nil
	}
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return []string{""}
	}
	return strings.Split(text, "\n")
}

// Markdown renders the shell inventory as a compact pre-migration note.
func (i ShellFileInventory) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Shell File Inventory\n\n")
	fmt.Fprintf(&b, "- Repo: `%s`\n", mdEscape(i.RepoPath))
	fmt.Fprintf(&b, "- File: `%s`\n", mdEscape(i.Path))
	fmt.Fprintf(&b, "- Lines: `%d`\n", i.Lines)
	fmt.Fprintf(&b, "- Functions: `%d`\n", len(i.Functions))
	if len(i.Entrypoints) > 0 {
		fmt.Fprintf(&b, "- Entrypoints: %s\n", shellFunctionList(i.Entrypoints))
	} else {
		fmt.Fprintf(&b, "- Entrypoints: `none`\n")
	}
	if len(i.SourcedBy) > 0 {
		fmt.Fprintf(&b, "- Sourced by: %s\n", shellCallerList(i.SourcedBy, 8))
	}
	if len(i.Functions) > 0 {
		fmt.Fprintf(&b, "\n## Functions\n\n")
		fmt.Fprintf(&b, "| Function | Line | Obvious Callers |\n")
		fmt.Fprintf(&b, "|---|---:|---|\n")
		for _, fn := range i.Functions {
			fmt.Fprintf(&b, "| `%s` | `%d` | %s |\n", mdEscape(fn.Name), fn.Line, shellCallerList(fn.Callers, 4))
		}
	}
	return b.String()
}

func shellFunctionList(functions []ShellFunction) string {
	parts := make([]string, 0, len(functions))
	for _, fn := range functions {
		parts = append(parts, fmt.Sprintf("`%s`", mdEscape(fn.Name)))
	}
	return strings.Join(parts, ", ")
}

func shellCallerList(callers []ShellCaller, limit int) string {
	if len(callers) == 0 {
		return "`none`"
	}
	parts := make([]string, 0, limit)
	for i, caller := range callers {
		if i >= limit {
			break
		}
		parts = append(parts, fmt.Sprintf("`%s:%d`", mdEscape(caller.Path), caller.Line))
	}
	if len(callers) > limit {
		parts = append(parts, fmt.Sprintf("and %d more", len(callers)-limit))
	}
	return strings.Join(parts, ", ")
}
