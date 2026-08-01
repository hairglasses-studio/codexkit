package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hairglasses-studio/codexkit/skillsync"
	"github.com/hairglasses-studio/codexkit/sourcecontract"
	"github.com/hairglasses-studio/codexkit/workspace"
)

type AuditReport struct {
	Timestamp     time.Time           `json:"timestamp"`
	WorkspaceRoot string              `json:"workspace_root"`
	Repos         []string            `json:"repos"`
	Skills        []string            `json:"skills"`
	Scripts       []string            `json:"scripts"`
	LLMSurfaces   map[string][]string `json:"llm_surfaces"`
}

var excludedTreeNames = map[string]struct{}{
	"archive":              {},
	"archives":             {},
	"build":                {},
	"content":              {},
	"data":                 {},
	"dist":                 {},
	"fixtures":             {},
	"git-cleanup-archives": {},
	"imported":             {},
	"node_modules":         {},
	"research":             {},
	"snapshots":            {},
	"target":               {},
	"testdata":             {},
	"third_party":          {},
	"vault":                {},
	"vendor":               {},
	"worktrees":            {},
}

var providerTreeNames = map[string]struct{}{
	".agents": {},
	".agy":    {},
	".claude": {},
	".codex":  {},
	".gemini": {},
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("studio-surface-audit", flag.ContinueOnError)
	flags.SetOutput(stderr)

	workspaceRoot := workspace.DefaultRoot()
	outputJSON := "surface-audit.json"
	outputMD := "surface-audit.md"
	checkMode := false
	flags.StringVar(&workspaceRoot, "workspace", workspaceRoot, "Workspace root to audit")
	flags.StringVar(&outputJSON, "json", outputJSON, "JSON output file")
	flags.StringVar(&outputMD, "md", outputMD, "Markdown output file")
	flags.BoolVar(&checkMode, "check", checkMode, "Run continuous source-contract, skill-link, and gofmt checks")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected arguments: %s\n", strings.Join(flags.Args(), " "))
		return 2
	}

	workspaceRoot = filepath.Clean(workspaceRoot)
	if checkMode {
		if runComplianceChecks(workspaceRoot, stdout) {
			return 0
		}
		return 1
	}

	report, err := collectAudit(workspaceRoot)
	if err != nil {
		fmt.Fprintf(stderr, "surface audit failed: %v\n", err)
		return 1
	}
	if err := writeAudit(report, outputJSON, outputMD); err != nil {
		fmt.Fprintf(stderr, "surface audit failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Audit completed successfully. Wrote to %s and %s\n", outputJSON, outputMD)
	return 0
}

func collectAudit(workspaceRoot string) (AuditReport, error) {
	report := AuditReport{
		Timestamp:     time.Now(),
		WorkspaceRoot: workspaceRoot,
		LLMSurfaces:   make(map[string][]string),
	}

	repoRoots, err := managedRepoRoots(workspaceRoot)
	if err != nil {
		return AuditReport{}, err
	}
	for _, root := range repoRoots {
		report.Repos = append(report.Repos, filepath.Base(root))
	}

	skillsDir := filepath.Join(workspaceRoot, ".agents", "skills")
	if entries, err := os.ReadDir(skillsDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				report.Skills = append(report.Skills, entry.Name())
			}
		}
	} else if !os.IsNotExist(err) {
		return AuditReport{}, fmt.Errorf("read workspace skills: %w", err)
	}

	if err := walkAuditRoot(workspaceRoot, workspaceRoot, true, &report); err != nil {
		return AuditReport{}, err
	}
	for _, repoRoot := range repoRoots {
		if err := walkAuditRoot(workspaceRoot, repoRoot, false, &report); err != nil {
			return AuditReport{}, err
		}
	}

	sort.Strings(report.Repos)
	sort.Strings(report.Skills)
	sort.Strings(report.Scripts)
	for provider := range report.LLMSurfaces {
		sort.Strings(report.LLMSurfaces[provider])
	}
	return report, nil
}

func managedRepoRoots(workspaceRoot string) ([]string, error) {
	manifest, err := workspace.LoadManifest(workspaceRoot)
	if err == nil {
		roots := make([]string, 0, len(manifest.Repos))
		for _, repo := range manifest.Repos {
			root := filepath.Join(workspaceRoot, repo.Name)
			if isGitRepo(root) {
				roots = append(roots, root)
			}
		}
		return roots, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("load workspace manifest: %w", err)
	}

	entries, readErr := os.ReadDir(workspaceRoot)
	if readErr != nil {
		return nil, fmt.Errorf("read workspace root: %w", readErr)
	}
	roots := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			root := filepath.Join(workspaceRoot, entry.Name())
			if isGitRepo(root) {
				roots = append(roots, root)
			}
		}
	}
	sort.Strings(roots)
	return roots, nil
}

func isGitRepo(root string) bool {
	gitPath := filepath.Join(root, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return false
	}
	if info.IsDir() {
		_, err = os.Stat(filepath.Join(gitPath, "HEAD"))
		return err == nil
	}
	data, err := os.ReadFile(gitPath)
	return err == nil && strings.HasPrefix(strings.TrimSpace(string(data)), "gitdir:")
}

func walkAuditRoot(workspaceRoot, scanRoot string, workspaceOnly bool, report *AuditReport) error {
	return filepath.WalkDir(scanRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path == scanRoot {
				return nil
			}
			name := entry.Name()
			if workspaceOnly && filepath.Dir(path) == scanRoot {
				if _, allowed := providerTreeNames[name]; !allowed && name != "scripts" && name != "workspace" {
					return filepath.SkipDir
				}
			}
			if _, excluded := excludedTreeNames[name]; excluded {
				return filepath.SkipDir
			}
			if strings.HasPrefix(name, ".") {
				if _, allowed := providerTreeNames[name]; !allowed {
					return filepath.SkipDir
				}
			}
			return nil
		}

		rel, err := filepath.Rel(workspaceRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasSuffix(entry.Name(), ".sh") || strings.HasSuffix(entry.Name(), ".py") {
			report.Scripts = append(report.Scripts, rel)
		}
		if provider := providerForPath(rel, entry.Name()); provider != "" {
			report.LLMSurfaces[provider] = append(report.LLMSurfaces[provider], path)
		}
		return nil
	})
}

func providerForPath(rel, base string) string {
	lowerBase := strings.ToLower(base)
	switch {
	case lowerBase == "claude.md" || lowerBase == ".claude.json" || hasPathSegment(rel, ".claude"):
		return "claude"
	case lowerBase == "codex.md" || lowerBase == "agents.md" || lowerBase == ".codex.json" || hasPathSegment(rel, ".codex"):
		return "codex"
	case lowerBase == "gemini.md" || lowerBase == ".gemini.json" || hasPathSegment(rel, ".gemini"):
		return "gemini"
	case lowerBase == "agy.md" || lowerBase == ".agy.json" || hasPathSegment(rel, ".agy"):
		return "agy"
	default:
		return ""
	}
}

func hasPathSegment(path, segment string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == segment {
			return true
		}
	}
	return false
}

func writeAudit(report AuditReport, outputJSON, outputMD string) error {
	jsonBytes, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode JSON report: %w", err)
	}
	if err := writeReportFile(outputJSON, append(jsonBytes, '\n')); err != nil {
		return fmt.Errorf("write JSON report: %w", err)
	}

	var md strings.Builder
	fmt.Fprintf(&md, "# Surface Audit Report\n\nGenerated: %s\nWorkspace: %s\n\n", report.Timestamp.Format(time.RFC3339), report.WorkspaceRoot)
	fmt.Fprintf(&md, "## Repositories (%d)\n", len(report.Repos))
	for _, repo := range report.Repos {
		fmt.Fprintf(&md, "- %s\n", repo)
	}
	fmt.Fprintf(&md, "\n## Skills (%d)\n", len(report.Skills))
	for _, skill := range report.Skills {
		fmt.Fprintf(&md, "- %s\n", skill)
	}
	fmt.Fprintf(&md, "\n## Scripts (%d)\n", len(report.Scripts))
	for _, script := range report.Scripts {
		fmt.Fprintf(&md, "- %s\n", script)
	}
	md.WriteString("\n## LLM Surfaces\n")
	for _, provider := range []string{"claude", "codex", "gemini", "agy"} {
		surfaces := report.LLMSurfaces[provider]
		fmt.Fprintf(&md, "### %s (%d)\n", provider, len(surfaces))
		for _, surface := range surfaces {
			fmt.Fprintf(&md, "- %s\n", surface)
		}
	}
	if err := writeReportFile(outputMD, []byte(md.String())); err != nil {
		return fmt.Errorf("write Markdown report: %w", err)
	}
	return nil
}

func writeReportFile(path string, content []byte) error {
	if parent := filepath.Dir(path); parent != "." {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, content, 0o644)
}

func runComplianceChecks(workspaceRoot string, out io.Writer) bool {
	fmt.Fprintln(out, "=== Studio Continuous Unification Compliance Checks ===")
	passed := true

	fmt.Fprintln(out, "--> Running Source Contract Check...")
	report, err := sourcecontract.Check(workspaceRoot, sourcecontract.CheckOptions{
		SkipRuntimeInventory: true,
		SkillValidatorMode:   skillsync.ValidatorOff,
	})
	if err != nil {
		fmt.Fprintf(out, "[FAIL] Source contract check error: %v\n", err)
		passed = false
	} else if !report.Passed {
		fmt.Fprintln(out, "[FAIL] Source contract check failed")
		printSourceContractFailures(out, report)
		passed = false
	} else {
		fmt.Fprintln(out, "[PASS] Source contract check passed")
	}

	fmt.Fprintln(out, "--> Running Skill Symlink Integrity Check...")
	if !checkSkillSymlinks(workspaceRoot, out) {
		passed = false
	} else {
		fmt.Fprintln(out, "[PASS] Skill symlink integrity check passed")
	}

	fmt.Fprintln(out, "--> Running Gofmt Compliance Check...")
	if !checkGofmtCompliance(workspaceRoot, out) {
		passed = false
	} else {
		fmt.Fprintln(out, "[PASS] Gofmt compliance check passed")
	}

	if passed {
		fmt.Fprintln(out, "=== ALL COMPLIANCE CHECKS PASSED ===")
	} else {
		fmt.Fprintln(out, "=== COMPLIANCE CHECKS FAILED ===")
	}
	return passed
}

func printSourceContractFailures(out io.Writer, report sourcecontract.Report) {
	for _, finding := range report.Workspace.Findings {
		if !finding.Passed {
			fmt.Fprintf(out, "  - workspace/%s %s: %s\n", finding.Check, finding.Repo, finding.Message)
		}
	}
	for _, repo := range report.Repos {
		if repo.Passed {
			continue
		}
		for _, err := range repo.Errors {
			fmt.Fprintf(out, "  - %s: %s\n", repo.Repo, err)
		}
		if repo.SkillSync != nil && repo.SkillSync.PendingChanges {
			fmt.Fprintf(out, "  - %s: skill surface drift\n", repo.Repo)
		}
		if repo.MCPSync != nil && repo.MCPSync.PendingChanges {
			fmt.Fprintf(out, "  - %s: MCP surface drift\n", repo.Repo)
		}
	}
}

func checkSkillSymlinks(workspaceRoot string, out io.Writer) bool {
	roots, err := managedRepoRoots(workspaceRoot)
	if err != nil {
		fmt.Fprintf(out, "[FAIL] Discover managed repos: %v\n", err)
		return false
	}
	roots = append([]string{workspaceRoot}, roots...)
	passed := true
	checked := 0
	for _, root := range roots {
		for _, providerDir := range []string{".claude/skills", ".codex/skills", ".gemini/skills"} {
			targetDir := filepath.Join(root, providerDir)
			if _, err := os.Stat(targetDir); err != nil {
				if os.IsNotExist(err) {
					continue
				}
				fmt.Fprintf(out, "[FAIL] Inspect skill directory %s: %v\n", targetDir, err)
				passed = false
				continue
			}
			walkErr := filepath.WalkDir(targetDir, func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				info, err := os.Lstat(path)
				if err != nil {
					return err
				}
				if info.Mode()&os.ModeSymlink == 0 {
					return nil
				}
				checked++
				_, err = filepath.EvalSymlinks(path)
				if err != nil {
					fmt.Fprintf(out, "[FAIL] Broken skill symlink: %s: %v\n", path, err)
					passed = false
					return nil
				}
				return nil
			})
			if walkErr != nil {
				fmt.Fprintf(out, "[FAIL] Walk skill directory %s: %v\n", targetDir, walkErr)
				passed = false
			}
		}
	}
	fmt.Fprintf(out, "    Verified %d skill symlinks\n", checked)
	return passed
}

func checkGofmtCompliance(workspaceRoot string, out io.Writer) bool {
	roots, err := managedRepoRoots(workspaceRoot)
	if err != nil {
		fmt.Fprintf(out, "[FAIL] Discover managed repos: %v\n", err)
		return false
	}
	passed := true
	checked := 0
	for _, root := range roots {
		files, err := trackedGoFiles(root)
		if err != nil {
			fmt.Fprintf(out, "[FAIL] List tracked Go files in %s: %v\n", root, err)
			passed = false
			continue
		}
		for _, rel := range files {
			if excludedRelativePath(rel) {
				continue
			}
			checked++
			path := filepath.Join(root, filepath.FromSlash(rel))
			content, err := os.ReadFile(path)
			if err != nil {
				fmt.Fprintf(out, "[FAIL] Read Go file %s: %v\n", path, err)
				passed = false
				continue
			}
			formatted, err := format.Source(content)
			if err != nil {
				fmt.Fprintf(out, "[FAIL] Gofmt parse error in %s: %v\n", path, err)
				passed = false
				continue
			}
			if !bytes.Equal(content, formatted) {
				relWorkspace, _ := filepath.Rel(workspaceRoot, path)
				fmt.Fprintf(out, "[FAIL] Unformatted Go file: %s\n", filepath.ToSlash(relWorkspace))
				passed = false
			}
		}
	}
	fmt.Fprintf(out, "    Checked %d tracked Go source files\n", checked)
	return passed
}

func trackedGoFiles(repoRoot string) ([]string, error) {
	cmd := exec.Command("git", "-C", repoRoot, "ls-files", "-z", "--", "*.go")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	raw := bytes.Split(output, []byte{0})
	files := make([]string, 0, len(raw))
	for _, item := range raw {
		if len(item) > 0 {
			files = append(files, filepath.ToSlash(string(item)))
		}
	}
	return files, nil
}

func excludedRelativePath(rel string) bool {
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if _, excluded := excludedTreeNames[part]; excluded {
			return true
		}
	}
	return false
}
