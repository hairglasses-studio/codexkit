package main

import (
	"strings"
	"testing"

	"github.com/hairglasses-studio/codexkit/baselineguard"
)

func TestBaselineFailureHintsIncludesRepoScopedCommands(t *testing.T) {
	hints := baselineFailureHints("/tmp/hairglasses-studio/dotfiles", []baselineguard.Finding{
		{
			Check:   "skill_sync",
			Message: "update Claude skill",
			Remediation: []baselineguard.Remediation{{
				Kind:    "generator",
				Message: "regenerate skill mirrors",
				Command: []string{
					"codexkit",
					"skills",
					"sync",
					"/tmp/hairglasses-studio/dotfiles",
					"--quiet-warnings",
				},
			}},
		},
		{
			Check:   "skill_sync",
			Message: "update another Claude skill",
			Remediation: []baselineguard.Remediation{{
				Kind:    "generator",
				Message: "regenerate skill mirrors",
				Command: []string{
					"codexkit",
					"skills",
					"sync",
					"/tmp/hairglasses-studio/dotfiles",
					"--quiet-warnings",
				},
			}},
		},
		{
			Check:   "mcp_sync",
			Message: "update MCP block",
			Remediation: []baselineguard.Remediation{{
				Kind:    "generator",
				Message: "regenerate MCP config",
				Command: []string{"codexkit", "mcp", "sync", "/tmp/hairglasses-studio/dotfiles"},
			}},
		},
		{
			Check:   "canonical_agents",
			Message: "missing canonical marker",
			Remediation: []baselineguard.Remediation{{
				Kind:    "edit",
				Message: "restore canonical provider instruction files: AGENTS.md, CLAUDE.md, GEMINI.md",
			}},
		},
		{
			Check:   "project_local_profiles",
			Message: "unsupported profiles",
			Remediation: []baselineguard.Remediation{{
				Kind:    "edit",
				Message: "edit .codex/config.toml; remove unsupported [profiles.*] tables",
			}},
		},
	})

	got := strings.Join(hints, "\n")
	for _, want := range []string{
		"baseline check '/tmp/hairglasses-studio/dotfiles'",
		"skills sync /tmp/hairglasses-studio/dotfiles --quiet-warnings",
		"mcp sync /tmp/hairglasses-studio/dotfiles",
		"AGENTS.md, CLAUDE.md, GEMINI.md",
		"remove unsupported [profiles.*] tables",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("baseline hints missing %q in:\n%s", want, got)
		}
	}

	if count := strings.Count(got, "skills sync"); count != 1 {
		t.Fatalf("expected one skills sync hint, got %d in:\n%s", count, got)
	}
}

func TestShellQuoteEscapesSingleQuotes(t *testing.T) {
	got := shellQuote("/tmp/studio/owner's repo")
	want := "'/tmp/studio/owner'\"'\"'s repo'"
	if got != want {
		t.Fatalf("shellQuote() = %q, want %q", got, want)
	}
}
