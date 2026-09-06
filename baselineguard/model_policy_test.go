package baselineguard

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFleetModelPolicyIsNotUpstreamRetirement(t *testing.T) {
	r := &Report{}
	r.addModelPinVerdict(modelLifecycleEntry{ID: "legacy", PolicyDisallowed: true, Replacement: "current"}, ".codex/config.toml", time.Now())
	if len(r.Findings) != 1 || r.Findings[0].Passed || !strings.Contains(r.Findings[0].Message, "fleet model policy") || strings.Contains(r.Findings[0].Message, "retired") {
		t.Fatalf("local policy misrepresented: %+v", r.Findings)
	}
}

func TestModelPolicyResolvesWorkspaceForIsolatedCheckout(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEXKIT_WORKSPACE_ROOT", root)
	writeFile(t, root, "workspace/model-lifecycle.json", `{"version":1,"models":[{"id":"legacy","policy_disallowed":true,"replacement":"current"}]}`)
	repo := filepath.Join(t.TempDir(), "isolated")
	writeFile(t, repo, ".codex/config.toml", "model = \"legacy\"\n")
	r := &Report{}
	r.addModelPinFreshness(repo)
	if len(r.Findings) != 1 || r.Findings[0].Passed {
		t.Fatalf("isolated checkout skipped policy: %+v", r.Findings)
	}
	writeFile(t, root, "workspace/model-lifecycle.json", "{")
	r = &Report{}
	r.addModelPinFreshness(repo)
	if len(r.Findings) != 1 || r.Findings[0].Passed {
		t.Fatal("invalid registry passed")
	}
}
