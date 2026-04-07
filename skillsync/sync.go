// Package skillsync synchronizes skill definitions from .agents/skills/
// to .claude/skills/ and plugins/ mirrors.
//
// It supports the Agent Skills open standard (Dec 2025) and hot-reloading
// skills (Jan 2026).
package skillsync

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hairglasses-studio/codexkit"
)

// SyncAction describes a single file operation.
type SyncAction struct {
	Action  string `json:"action"`  // "create", "update", "unchanged"
	SrcPath string `json:"src"`     // source path
	DstPath string `json:"dst"`     // destination path
	Message string `json:"message"` // human-readable description
}

// SyncReport is the result of a skill sync operation.
type SyncReport struct {
	RepoPath string       `json:"repo_path"`
	DryRun   bool         `json:"dry_run"`
	Actions  []SyncAction `json:"actions"`
	Errors   []string     `json:"errors,omitempty"`
}

// SkillEntry represents a skill from surface.yaml.
type SkillEntry struct {
	Name string `json:"name"`
}

// Surface represents the surface.yaml file.
type Surface struct {
	Version int          `json:"version"`
	Skills  []SkillEntry `json:"skills"`
}

// ParseSurface reads and parses the surface.yaml file.
func ParseSurface(repoPath string) (*Surface, error) {
	surfacePath := filepath.Join(repoPath, ".agents/skills/surface.yaml")
	data, err := os.ReadFile(surfacePath)
	if err != nil {
		return nil, fmt.Errorf("reading surface.yaml: %w", err)
	}

	var surface Surface
	if err := json.Unmarshal(data, &surface); err != nil {
		// Try YAML-style parsing
		content := string(data)
		if strings.Contains(content, "version: 1") || strings.Contains(content, "\"version\": 1") {
			surface.Version = 1
			for _, line := range strings.Split(content, "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "- name:") {
					name := strings.TrimSpace(strings.TrimPrefix(trimmed, "- name:"))
					name = strings.Trim(name, "\"'")
					surface.Skills = append(surface.Skills, SkillEntry{Name: name})
				}
			}
		} else {
			return nil, fmt.Errorf("invalid surface.yaml: %w", err)
		}
	}
	if surface.Version != 1 {
		return nil, fmt.Errorf("unsupported surface version: %d", surface.Version)
	}
	return &surface, nil
}

// FilterPortableFrontmatter keeps only portable keys in SKILL.md frontmatter
// per the Agent Skills open standard.
func FilterPortableFrontmatter(content string) string {
	if !strings.HasPrefix(content, "---\n") {
		return content
	}
	endIdx := strings.Index(content[4:], "\n---")
	if endIdx < 0 {
		return content
	}
	frontmatter := content[4 : 4+endIdx]
	body := content[4+endIdx+4:] // after closing ---

	var filtered []string
	for _, line := range strings.Split(frontmatter, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) < 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		if codexkit.PortableFrontmatterKeys[key] {
			filtered = append(filtered, line)
		}
	}
	if len(filtered) == 0 {
		return strings.TrimLeft(body, "\n")
	}
	return "---\n" + strings.Join(filtered, "\n") + "\n---" + body
}

// Sync performs skill synchronization for a repository.
func Sync(repoPath string, dryRun bool) SyncReport {
	report := SyncReport{RepoPath: repoPath, DryRun: dryRun}

	surface, err := ParseSurface(repoPath)
	if err != nil {
		report.Errors = append(report.Errors, err.Error())
		return report
	}

	for _, skill := range surface.Skills {
		srcPath := filepath.Join(repoPath, ".agents/skills", skill.Name, "SKILL.md")
		srcData, err := os.ReadFile(srcPath)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("reading %s: %v", srcPath, err))
			continue
		}

		// Sync to .claude/skills/
		claudeDst := filepath.Join(repoPath, ".claude/skills", skill.Name, "SKILL.md")
		report.syncFile(srcData, claudeDst, dryRun)

		// Sync to plugins/ mirror
		pluginDst := filepath.Join(repoPath, "plugins", skill.Name, "SKILL.md")
		filteredContent := FilterPortableFrontmatter(string(srcData))
		report.syncFiltered([]byte(filteredContent), pluginDst, dryRun)
	}

	return report
}

func (r *SyncReport) syncFile(srcData []byte, dstPath string, dryRun bool) {
	dstData, err := os.ReadFile(dstPath)
	if err != nil {
		// File doesn't exist — create it
		r.Actions = append(r.Actions, SyncAction{
			Action:  "create",
			DstPath: dstPath,
			Message: fmt.Sprintf("create %s", dstPath),
		})
		if !dryRun {
			if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
				r.Errors = append(r.Errors, fmt.Sprintf("mkdir %s: %v", filepath.Dir(dstPath), err))
				return
			}
			if err := os.WriteFile(dstPath, srcData, 0644); err != nil {
				r.Errors = append(r.Errors, fmt.Sprintf("writing %s: %v", dstPath, err))
			}
		}
		return
	}
	if string(dstData) == string(srcData) {
		r.Actions = append(r.Actions, SyncAction{
			Action:  "unchanged",
			DstPath: dstPath,
			Message: fmt.Sprintf("unchanged %s", dstPath),
		})
		return
	}
	r.Actions = append(r.Actions, SyncAction{
		Action:  "update",
		DstPath: dstPath,
		Message: fmt.Sprintf("update %s", dstPath),
	})
	if !dryRun {
		if err := os.WriteFile(dstPath, srcData, 0644); err != nil {
			r.Errors = append(r.Errors, fmt.Sprintf("writing %s: %v", dstPath, err))
		}
	}
}

func (r *SyncReport) syncFiltered(content []byte, dstPath string, dryRun bool) {
	dstData, err := os.ReadFile(dstPath)
	if err != nil {
		r.Actions = append(r.Actions, SyncAction{
			Action:  "create",
			DstPath: dstPath,
			Message: fmt.Sprintf("create %s (filtered)", dstPath),
		})
		if !dryRun {
			if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
				r.Errors = append(r.Errors, fmt.Sprintf("mkdir %s: %v", filepath.Dir(dstPath), err))
				return
			}
			if err := os.WriteFile(dstPath, content, 0644); err != nil {
				r.Errors = append(r.Errors, fmt.Sprintf("writing %s: %v", dstPath, err))
			}
		}
		return
	}
	if string(dstData) == string(content) {
		r.Actions = append(r.Actions, SyncAction{
			Action:  "unchanged",
			DstPath: dstPath,
			Message: fmt.Sprintf("unchanged %s", dstPath),
		})
		return
	}
	r.Actions = append(r.Actions, SyncAction{
		Action:  "update",
		DstPath: dstPath,
		Message: fmt.Sprintf("update %s (filtered)", dstPath),
	})
	if !dryRun {
		if err := os.WriteFile(dstPath, content, 0644); err != nil {
			r.Errors = append(r.Errors, fmt.Sprintf("writing %s: %v", dstPath, err))
		}
	}
}

// Diff returns a report showing what would change without writing.
func Diff(repoPath string) SyncReport {
	return Sync(repoPath, true)
}

// List returns the skill names from the surface file.
func List(repoPath string) ([]string, error) {
	surface, err := ParseSurface(repoPath)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(surface.Skills))
	for i, s := range surface.Skills {
		names[i] = s.Name
	}
	return names, nil
}
