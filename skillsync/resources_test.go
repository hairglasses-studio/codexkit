package skillsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func resourceFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	source, target := filepath.Join(root, "source"), filepath.Join(root, "target")
	for _, dir := range []string{source, target} {
		writeFile(t, dir, "SKILL.md", "---\nname: resource-skill\ndescription: A resource workflow\n---\nRead references/workflow.md.\n")
	}
	writeFile(t, source, "references/workflow.md", "# source reference\n")
	return source, target
}

func TestResourcePlanApplyAndStaleRefusal(t *testing.T) {
	source, target := resourceFixture(t)
	plan := SyncResources(source, target, false, "")
	if len(plan.Errors) != 0 || len(plan.Links) != 1 || plan.Links[0].State != "missing" {
		t.Fatalf("plan: %+v", plan)
	}
	if _, err := os.Lstat(filepath.Join(target, "references")); !os.IsNotExist(err) {
		t.Fatal("plan wrote resources")
	}
	denied := SyncResources(source, target, true, "stale")
	if len(denied.Errors) == 0 {
		t.Fatal("stale plan accepted")
	}
	raw, _ := os.ReadFile(filepath.Join(target, "SKILL.md"))
	applied := SyncResources(source, target, true, plan.PlanSHA256)
	if len(applied.Errors) != 0 || !applied.Applied {
		t.Fatalf("apply: %+v", applied)
	}
	got, err := os.ReadFile(filepath.Join(target, "references/workflow.md"))
	if err != nil || string(got) != "# source reference\n" {
		t.Fatalf("reference missing: %s %v", got, err)
	}
	after, _ := os.ReadFile(filepath.Join(target, "SKILL.md"))
	if string(raw) != string(after) {
		t.Fatal("instructions changed")
	}
	again := SyncResources(source, target, false, "")
	if len(again.Errors) != 0 || again.Links[0].State != "linked" {
		t.Fatalf("not idempotent: %+v", again)
	}
}

func TestResourcePlanBindsInstructionsAndSourceBytes(t *testing.T) {
	for _, which := range []string{"source", "target", "resource"} {
		t.Run(which, func(t *testing.T) {
			source, target := resourceFixture(t)
			p := SyncResources(source, target, false, "")
			path := filepath.Join(source, "SKILL.md")
			if which == "target" {
				path = filepath.Join(target, "SKILL.md")
			}
			if which == "resource" {
				path = filepath.Join(source, "references/workflow.md")
			}
			if err := os.WriteFile(path, []byte("changed"), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := SyncResources(source, target, true, p.PlanSHA256); len(got.Errors) == 0 {
				t.Fatal("changed source/target accepted")
			}
			if _, err := os.Lstat(filepath.Join(target, "references")); !os.IsNotExist(err) {
				t.Fatal("refusal wrote target")
			}
		})
	}
}

func TestResourcesPreserveForeignDirectoryAndBrokenLink(t *testing.T) {
	for _, kind := range []string{"directory", "broken-link"} {
		t.Run(kind, func(t *testing.T) {
			source, target := resourceFixture(t)
			dst := filepath.Join(target, "references")
			if kind == "directory" {
				writeFile(t, target, "references/workflow.md", "foreign")
			} else if err := os.Symlink("missing", dst); err != nil {
				t.Fatal(err)
			}
			p := SyncResources(source, target, false, "")
			if len(p.Errors) == 0 {
				t.Fatal("foreign target accepted")
			}
			if kind == "directory" {
				b, _ := os.ReadFile(filepath.Join(dst, "workflow.md"))
				if string(b) != "foreign" {
					t.Fatal("foreign resource changed")
				}
			} else {
				s, _ := os.Readlink(dst)
				if s != "missing" {
					t.Fatal("broken link changed")
				}
			}
		})
	}
}

func TestResourcesRejectEscapeAndOversize(t *testing.T) {
	for _, kind := range []string{"escape", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			source, target := resourceFixture(t)
			if kind == "escape" {
				outside := filepath.Join(t.TempDir(), "private")
				if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(source, "references/escape")); err != nil {
					t.Fatal(err)
				}
			} else {
				f, err := os.Create(filepath.Join(source, "references/large"))
				if err != nil {
					t.Fatal(err)
				}
				if err := f.Truncate((8 << 20) + 1); err != nil {
					t.Fatal(err)
				}
				f.Close()
			}
			if p := SyncResources(source, target, false, ""); len(p.Errors) == 0 {
				t.Fatal("unsafe resource accepted")
			}
		})
	}
}

func TestResourcesRejectRedirectedSkillDirectory(t *testing.T) {
	source, target := resourceFixture(t)
	alias := target + "-alias"
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	if r := SyncResources(source, alias, false, ""); len(r.Errors) == 0 {
		t.Fatal("redirected target accepted")
	}
}

func TestRepoSyncIncludesResourcesAndPreflightsConflicts(t *testing.T) {
	dir := setupSyncRepo(t)
	writeFile(t, dir, ".agents/skills/myskill/references/workflow.md", "canonical")
	writeFile(t, dir, ".agents/skills/myskill/scripts/check.py", "print('ok')\n")
	dry := Diff(dir)
	if len(dry.Errors) > 0 {
		t.Fatal(dry.Errors)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude/skills/myskill/references")); !os.IsNotExist(err) {
		t.Fatal("dry run created references")
	}
	r := Sync(dir, false)
	if len(r.Errors) > 0 {
		t.Fatal(r.Errors)
	}
	for _, dest := range []string{".claude/skills/myskill", "plugins/demo/skills/myskill"} {
		got, err := os.ReadFile(filepath.Join(dir, dest, "references/workflow.md"))
		if err != nil || string(got) != "canonical" {
			t.Fatalf("resources missing in %s: %v", dest, err)
		}
	}
	writeFile(t, dir, ".agents/skills/myskill/SKILL.md", "---\nname: myskill\ndescription: changed description\n---\nchanged\n")
	target := filepath.Join(dir, ".claude/skills/myskill/references")
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, ".claude/skills/myskill/references/workflow.md", "foreign")
	before, _ := os.ReadFile(filepath.Join(dir, ".claude/skills/myskill/SKILL.md"))
	r = Sync(dir, false)
	if len(r.Errors) == 0 {
		t.Fatal("resource conflict did not refuse entire sync")
	}
	after, _ := os.ReadFile(filepath.Join(dir, ".claude/skills/myskill/SKILL.md"))
	if string(before) != string(after) {
		t.Fatal("instruction mutation preceded resource refusal")
	}
	if !strings.Contains(strings.Join(r.Errors, " "), "preserved") {
		t.Fatal(r.Errors)
	}
}
