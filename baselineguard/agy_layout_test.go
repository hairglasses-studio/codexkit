package baselineguard

import (
	"strings"
	"testing"
)

func TestAGYDocumentedLayouts(t *testing.T) {
	for _, path := range []string{"flat.md", "nested/agent.md"} {
		t.Run(path, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, ".agents/agents/"+path, "---\nname: auditor\ndescription: Audit surfaces.\n---\nInstructions\n")
			r := &Report{}
			r.addAGYAgentLayout(dir)
			if len(r.Findings) != 1 || !r.Findings[0].Passed {
				t.Fatalf("documented layout rejected: %+v", r.Findings)
			}
		})
	}
}

func TestAGYDuplicateIdentity(t *testing.T) {
	dir := t.TempDir()
	for _, path := range []string{"flat.md", "nested/agent.md"} {
		writeFile(t, dir, ".agents/agents/"+path, "---\nname: auditor\ndescription: Audit surfaces.\n---\n")
	}
	r := &Report{}
	r.addAGYAgentLayout(dir)
	for _, f := range r.Findings {
		if !f.Passed && strings.Contains(f.Message, "duplicate agent identity") {
			return
		}
	}
	t.Fatal("duplicate agent identities accepted")
}
