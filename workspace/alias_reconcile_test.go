package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func aliasFixture(t *testing.T) (string, []byte) {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "workspace"), 0o755); err != nil {
		t.Fatal(err)
	}
	aliasGitFixture(t, filepath.Join(root, "canonical"))
	if err := os.Symlink("canonical", filepath.Join(root, "staging")); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"version":1,"custom":{"unknown":9007199254740993,"values":[true,"keep"]},"repos":[{"name":"staging","lifecycle":"active","unknown_alias_field":"preserved-in-preimage"},{"name":"canonical","lifecycle":"active","future":{"n":9007199254740993}},{"name":"unrelated","lifecycle":"archived","metadata":[1,2,3]}]}`)
	if err := os.WriteFile(ManifestPath(root), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return root, raw
}

func aliasGitFixture(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("git", "-C", path, "init", "-q")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "GIT_TERMINAL_PROMPT=0")
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("fixture git init: %v %s", err, out)
	}
}

func aliasRequest(root string) AliasReconcileRequest {
	return AliasReconcileRequest{Root: root, Aliases: []string{"staging"}}
}

func TestAliasDryRunAndApplyPreserveUnknownFieldsPathsModeAndPreimage(t *testing.T) {
	for _, mode := range []os.FileMode{0o600, 0o644} {
		t.Run(mode.String(), func(t *testing.T) {
			root, raw := aliasFixture(t)
			if err := os.Chmod(ManifestPath(root), mode); err != nil {
				t.Fatal(err)
			}
			request := aliasRequest(root)
			plan, err := ReconcileAliases(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if plan.State != "PLANNED" || plan.WritesPerformed || plan.BeforeSHA256 != aliasDigest(raw) || len(plan.Removals) != 1 || plan.Removals[0].Canonical != "canonical" {
				t.Fatalf("invalid plan %+v", plan)
			}
			entries, _ := os.ReadDir(filepath.Join(root, "workspace"))
			if len(entries) != 1 {
				t.Fatal("dry run wrote state")
			}
			request.Apply = true
			request.ExpectedSHA256 = plan.BeforeSHA256
			result, err := ReconcileAliases(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if result.State != "APPLIED" || !result.WritesPerformed || result.AfterSHA256 != plan.AfterSHA256 {
				t.Fatalf("invalid apply %+v", result)
			}
			current, _ := os.ReadFile(ManifestPath(root))
			if aliasDigest(current) != plan.AfterSHA256 {
				t.Fatal("after mismatch")
			}
			var beforeDoc, afterDoc map[string]json.RawMessage
			json.Unmarshal(raw, &beforeDoc)
			json.Unmarshal(current, &afterDoc)
			var beforeUnknown, afterUnknown any
			json.Unmarshal(beforeDoc["custom"], &beforeUnknown)
			json.Unmarshal(afterDoc["custom"], &afterUnknown)
			b, _ := json.Marshal(beforeUnknown)
			a, _ := json.Marshal(afterUnknown)
			if !bytes.Equal(b, a) || !bytes.Contains(current, []byte("9007199254740993")) {
				t.Fatal("unknown fields or numeric precision lost")
			}
			var rows []map[string]json.RawMessage
			json.Unmarshal(afterDoc["repos"], &rows)
			if len(rows) != 2 || string(rows[0]["name"]) != `"canonical"` || string(rows[1]["name"]) != `"unrelated"` || rows[0]["future"] == nil || rows[1]["metadata"] == nil {
				t.Fatal("unrelated row order/fields lost")
			}
			backup, _ := os.ReadFile(result.PreimagePath)
			if !bytes.Equal(backup, raw) {
				t.Fatal("preimage not retained exactly")
			}
			info, _ := os.Stat(ManifestPath(root))
			if info.Mode().Perm() != mode {
				t.Fatal("manifest permissions changed")
			}
			if link, err := os.Readlink(filepath.Join(root, "staging")); err != nil || link != "canonical" {
				t.Fatal("symlink modified")
			}
			if err = verifyAliasGitRoot(context.Background(), filepath.Join(root, "canonical")); err != nil {
				t.Fatal("canonical checkout changed")
			}
			receipt, _ := os.ReadFile(result.ReceiptPath)
			var journal AliasReconcileReport
			json.Unmarshal(receipt, &journal)
			if journal.State != "PREPARED" || journal.BeforeSHA256 != plan.BeforeSHA256 || journal.AfterSHA256 != plan.AfterSHA256 || journal.PreimagePath != result.PreimagePath {
				t.Fatal("missing durable intent")
			}
			if _, err = ReconcileAliases(context.Background(), request); err == nil {
				t.Fatal("stale replay accepted after row removed")
			}
		})
	}
}

func TestAliasStaleHashRefusesBeforeWrites(t *testing.T) {
	root, raw := aliasFixture(t)
	request := aliasRequest(root)
	request.Apply = true
	request.ExpectedSHA256 = strings.Repeat("0", 64)
	if _, err := ReconcileAliases(context.Background(), request); err == nil {
		t.Fatal("stale hash accepted")
	}
	current, _ := os.ReadFile(ManifestPath(root))
	if !bytes.Equal(raw, current) {
		t.Fatal("stale write")
	}
	entries, _ := os.ReadDir(filepath.Join(root, "workspace"))
	if len(entries) != 1 {
		t.Fatal("stale request wrote artifacts")
	}
}

func TestAliasConcurrentManifestAndMappingChangesRefuse(t *testing.T) {
	for _, kind := range []string{"manifest", "alias", "target"} {
		t.Run(kind, func(t *testing.T) {
			root, raw := aliasFixture(t)
			request := aliasRequest(root)
			request.Apply = true
			request.ExpectedSHA256 = aliasDigest(raw)
			changed := raw
			hook := func() {
				switch kind {
				case "manifest":
					changed = append(append([]byte(nil), raw...), byte('\n'))
					if err := os.WriteFile(ManifestPath(root), changed, 0o600); err != nil {
						t.Fatal(err)
					}
				case "alias":
					os.Remove(filepath.Join(root, "staging"))
					if err := os.Symlink(filepath.Join(root, "canonical"), filepath.Join(root, "staging")); err != nil {
						t.Fatal(err)
					}
				case "target":
					if err := os.Rename(filepath.Join(root, "canonical"), filepath.Join(root, "old")); err != nil {
						t.Fatal(err)
					}
					aliasGitFixture(t, filepath.Join(root, "canonical"))
				}
			}
			if _, err := reconcileAliases(context.Background(), request, hook); err == nil {
				t.Fatal("changed observation accepted")
			}
			current, _ := os.ReadFile(ManifestPath(root))
			if !bytes.Equal(current, changed) {
				t.Fatal("peer manifest changed")
			}
		})
	}
}

func TestAliasUnsafeOrAmbiguousCandidatesRefuse(t *testing.T) {
	for _, kind := range []string{"inactive_alias", "inactive_owner", "duplicate_name", "duplicate_owner", "missing_alias", "physical_alias", "outside", "non_git", "override"} {
		t.Run(kind, func(t *testing.T) {
			root, raw := aliasFixture(t)
			switch kind {
			case "inactive_alias":
				raw = bytes.Replace(raw, []byte(`"name":"staging","lifecycle":"active"`), []byte(`"name":"staging","lifecycle":"archived"`), 1)
			case "inactive_owner":
				raw = bytes.Replace(raw, []byte(`"name":"canonical","lifecycle":"active"`), []byte(`"name":"canonical","lifecycle":"archived"`), 1)
			case "duplicate_name":
				raw = bytes.Replace(raw, []byte(`"name":"unrelated"`), []byte(`"name":"canonical"`), 1)
			case "duplicate_owner":
				raw = bytes.Replace(raw, []byte(`"name":"unrelated","lifecycle":"archived"`), []byte(`"name":"unrelated","lifecycle":"active","path":"canonical"`), 1)
			case "missing_alias":
				os.Remove(filepath.Join(root, "staging"))
			case "physical_alias":
				os.Remove(filepath.Join(root, "staging"))
				aliasGitFixture(t, filepath.Join(root, "staging"))
			case "outside":
				os.Remove(filepath.Join(root, "staging"))
				if err := os.Symlink(t.TempDir(), filepath.Join(root, "staging")); err != nil {
					t.Fatal(err)
				}
			case "non_git":
				if err := os.RemoveAll(filepath.Join(root, "canonical", ".git")); err != nil {
					t.Fatal(err)
				}
			case "override":
				raw = bytes.Replace(raw, []byte(`"name":"staging"`), []byte(`"name":"staging","path":"canonical"`), 1)
			}
			if err := os.WriteFile(ManifestPath(root), raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReconcileAliases(context.Background(), aliasRequest(root)); err == nil {
				t.Fatal("unsafe candidate accepted")
			}
			current, _ := os.ReadFile(ManifestPath(root))
			if !bytes.Equal(raw, current) {
				t.Fatal("refusal changed manifest")
			}
		})
	}
}

func TestAliasMalformedDuplicateAndUnsafeManifestRefuse(t *testing.T) {
	for _, raw := range []string{`{"version":1,"version":1,"repos":[]}`, `{"version":1,"repos":null}`, `{"version":1,"repos":[{"name":"x","name":"y"}]}`, `{} {}`, `null`} {
		root, _ := aliasFixture(t)
		os.WriteFile(ManifestPath(root), []byte(raw), 0o600)
		if _, err := ReconcileAliases(context.Background(), aliasRequest(root)); err == nil {
			t.Fatal("malformed accepted")
		}
	}
	root, _ := aliasFixture(t)
	if err := os.Chmod(ManifestPath(root), 0o664); err != nil {
		t.Fatal(err)
	}
	if _, err := ReconcileAliases(context.Background(), aliasRequest(root)); err == nil {
		t.Fatal("writable manifest accepted")
	}
	root, _ = aliasFixture(t)
	path := ManifestPath(root)
	if err := os.Rename(path, path+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path+".original", path); err != nil {
		t.Fatal(err)
	}
	if _, err := ReconcileAliases(context.Background(), aliasRequest(root)); err == nil {
		t.Fatal("manifest symlink followed")
	}
}

func TestAliasTypedMCPMatchesDryRunAndRejectsWrongTypes(t *testing.T) {
	root, _ := aliasFixture(t)
	for _, tool := range Module().Tools() {
		if tool.Name == "workspace_manifest_reconcile_aliases" {
			result, err := tool.ContextHandler(context.Background(), map[string]any{"root": root, "aliases": []string{"staging"}})
			if err != nil || result.(AliasReconcileReport).State != "PLANNED" {
				t.Fatal("typed handler failed", err)
			}
			if _, err = tool.ContextHandler(context.Background(), map[string]any{"root": root, "aliases": []string{"staging"}, "apply": "true"}); err == nil {
				t.Fatal("wrong typed apply accepted")
			}
			return
		}
	}
	t.Fatal("typed MCP missing")
}

func TestAliasTwoConcurrentAppliesHaveOneWinner(t *testing.T) {
	root, raw := aliasFixture(t)
	req := aliasRequest(root)
	req.Apply = true
	req.ExpectedSHA256 = aliasDigest(raw)
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := reconcileAliases(context.Background(), req, func() { ready <- struct{}{}; <-release })
			results <- err
		}()
	}
	<-ready
	<-ready
	close(release)
	succeeded := 0
	for range 2 {
		if <-results == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("concurrent winners: %d", succeeded)
	}
}

func TestAliasGitProbeBoundsOutputAndInheritedPipe(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	git := filepath.Join(bin, "git")
	if err := os.WriteFile(git, []byte("#!/bin/sh\nhead -c 9000 /dev/zero\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := verifyAliasGitRoot(ctx, root); err == nil {
		t.Fatal("unbounded Git output accepted")
	}
	childPath := filepath.Join(root, "child.pid")
	wrapper := "#!/bin/sh\nsleep 5 &\nprintf '%s' \"$!\" > '" + childPath + "'\nprintf '%s\\n' \"$2\"\n"
	if err := os.WriteFile(git, []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		raw, err := os.ReadFile(childPath)
		if err == nil {
			pid, _ := strconv.Atoi(string(raw))
			if pid > 0 {
				process, _ := os.FindProcess(pid)
				_ = process.Kill()
			}
		}
	})
	start := time.Now()
	if err := verifyAliasGitRoot(context.Background(), root); err == nil {
		t.Fatal("inherited stdout accepted")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("Git pipe wait escaped bound")
	}
}

func TestAliasGitOutputCannotBypassCapThroughReaderFrom(t *testing.T) {
	var output aliasGitOutput
	_, err := io.Copy(&output, struct{ io.Reader }{strings.NewReader(strings.Repeat("x", 16384))})
	if err == nil || !output.exceeded || output.buffer.Len() > 8192 {
		t.Fatal("Git output escaped byte cap")
	}
}

func TestAliasAllowsExistingDotRepositoryButRefusesDotTraversal(t *testing.T) {
	root, raw := aliasFixture(t)
	raw = bytes.ReplaceAll(raw, []byte(`"unrelated"`), []byte(`".github"`))
	if err := os.WriteFile(ManifestPath(root), raw, 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := ReconcileAliases(context.Background(), aliasRequest(root))
	if err != nil || plan.State != "PLANNED" {
		t.Fatalf("dot repository: %+v %v", plan, err)
	}
	for _, name := range []string{".", "..", "../outside", "a/b"} {
		req := aliasRequest(root)
		req.Aliases = []string{name}
		if _, err := ReconcileAliases(context.Background(), req); err == nil {
			t.Fatalf("accepted traversal %q", name)
		}
	}
}
