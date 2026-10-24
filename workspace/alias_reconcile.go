package workspace

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/hairglasses-studio/codexkit/internal/harnessfs"
)

const aliasManifestLimit = 1 << 20

var aliasNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)
var aliasHashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type AliasReconcileRequest struct {
	Root           string   `json:"root"`
	Aliases        []string `json:"aliases"`
	ExpectedSHA256 string   `json:"expected_sha256,omitempty"`
	Apply          bool     `json:"apply,omitempty"`
}

type AliasRemoval struct {
	Alias      string `json:"alias"`
	Canonical  string `json:"canonical"`
	LinkTarget string `json:"link_target"`
	TargetPath string `json:"target_path"`
}

type AliasReconcileReport struct {
	StagingPath     string         `json:"staging_path,omitempty"`
	RetainedPath    string         `json:"retained_path,omitempty"`
	Schema          string         `json:"schema"`
	State           string         `json:"state"`
	Root            string         `json:"root"`
	BeforeSHA256    string         `json:"before_sha256"`
	AfterSHA256     string         `json:"after_sha256"`
	Removals        []AliasRemoval `json:"removals"`
	ManifestMode    string         `json:"manifest_mode"`
	ReceiptPath     string         `json:"receipt_path,omitempty"`
	PreimagePath    string         `json:"preimage_path,omitempty"`
	WritesPerformed bool           `json:"writes_performed"`
}

type aliasPlan struct {
	report        AliasReconcileReport
	before, after []byte
	entry         harnessfs.Entry
	identities    map[string]os.FileInfo
}

func aliasDigest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

// ReconcileAliases removes only explicitly named proven duplicate symlink rows.
// It never regenerates the manifest or removes repository paths.
func ReconcileAliases(ctx context.Context, req AliasReconcileRequest) (AliasReconcileReport, error) {
	return reconcileAliases(ctx, req, nil)
}

func reconcileAliases(ctx context.Context, req AliasReconcileRequest, beforeApply func()) (report AliasReconcileReport, resultErr error) {
	var empty AliasReconcileReport
	if !filepath.IsAbs(req.Root) || filepath.Clean(req.Root) != req.Root || req.Root == "/" || len(req.Aliases) < 1 || len(req.Aliases) > 32 {
		return empty, errors.New("explicit canonical workspace and 1..32 alias names required")
	}
	if (req.Apply && !aliasHashPattern.MatchString(req.ExpectedSHA256)) || (req.ExpectedSHA256 != "" && !aliasHashPattern.MatchString(req.ExpectedSHA256)) {
		return empty, errors.New("apply requires exact expected_sha256")
	}
	resolved, err := filepath.EvalSymlinks(req.Root)
	if err != nil || resolved != req.Root {
		return empty, errors.New("workspace root must be an existing physical path")
	}
	seen := map[string]bool{}
	for _, name := range req.Aliases {
		if (!aliasNamePattern.MatchString(name) || name == "." || name == "..") || seen[name] {
			return empty, errors.New("invalid or duplicate alias name")
		}
		seen[name] = true
	}
	req.Aliases = append([]string(nil), req.Aliases...)
	sort.Strings(req.Aliases)
	fs, err := harnessfs.Open(filepath.Join(req.Root, "workspace"))
	if err != nil {
		return empty, err
	}
	defer fs.Close()
	plan, err := planAliasRemoval(ctx, fs, req)
	if err != nil {
		return empty, err
	}
	if !req.Apply {
		return plan.report, nil
	}
	if beforeApply != nil {
		beforeApply()
	}
	unlock, err := lockAliasManifest(filepath.Join(req.Root, "workspace", ".manifest-alias-reconcile.lock"))
	if err != nil {
		return empty, err
	}
	defer unlock()
	current, err := planAliasRemoval(ctx, fs, req)
	if err != nil {
		return empty, err
	}
	if !sameAliasPlan(plan, current) {
		return empty, errors.New("manifest or alias mapping changed after observation")
	}
	token := make([]byte, 16)
	if _, err = rand.Read(token); err != nil {
		return empty, err
	}
	suffix := hex.EncodeToString(token)
	stageName := ".manifest-alias-stage-" + suffix
	preimageName := ".manifest-alias-preimage-" + suffix
	retainedName := ".manifest-alias-retained-" + suffix
	receiptName := ".manifest-alias-receipt-" + suffix + ".json"
	stage, err := fs.CreateStage("manifest.json", stageName)
	if err != nil {
		return empty, err
	}
	defer func() {
		if hold, err := stage.Detach(retainedName); err == nil {
			_ = hold.Close()
		}
		_ = stage.Close()
	}()
	report = plan.report
	report.State = "PREPARED"
	report.PreimagePath = filepath.Join(req.Root, "workspace", preimageName)
	report.StagingPath = filepath.Join(req.Root, "workspace", stageName)
	report.RetainedPath = filepath.Join(req.Root, "workspace", retainedName)
	report.ReceiptPath = filepath.Join(req.Root, "workspace", receiptName)
	report.WritesPerformed = true
	if _, err = stage.Write(plan.after); err != nil {
		return report, err
	}
	if err = stage.Chmod(plan.entry.Mode & 0o777); err != nil {
		return report, err
	}
	if err = stage.Sync(); err != nil {
		return report, err
	}
	receipt, err := fs.CreateStage(receiptName, ".manifest-alias-receipt-stage-"+suffix)
	if err != nil {
		return report, err
	}
	defer receipt.Close()
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return report, err
	}
	raw = append(raw, '\n')
	if _, err = receipt.Write(raw); err != nil {
		return report, err
	}
	if err = receipt.Sync(); err != nil {
		return report, err
	}
	if err = receipt.CommitCreate(); err != nil {
		return report, err
	}
	if err = fs.SyncParent(receiptName); err != nil {
		return report, err
	}
	// Recheck after staging. The owner lock coordinates this API's writers;
	// arbitrary external writers do not participate in it.
	final, err := planAliasRemoval(ctx, fs, req)
	if err != nil {
		return report, err
	}
	if !sameAliasPlan(plan, final) {
		return report, errors.New("manifest or aliases changed before exchange")
	}
	if err = stage.Exchange(); err != nil {
		report.State = "UNKNOWN"
		return report, err
	}
	hold, err := stage.Detach(preimageName)
	if err != nil {
		report.State = "UNKNOWN"
		return report, err
	}
	if err = hold.Close(); err != nil {
		report.State = "UNKNOWN"
		return report, err
	}
	if err = fs.SyncParent("manifest.json"); err != nil {
		report.State = "UNKNOWN"
		return report, err
	}
	after, _, err := fs.ReadRegular("manifest.json", aliasManifestLimit)
	if err != nil || aliasDigest(after) != report.AfterSHA256 {
		report.State = "UNKNOWN"
		return report, errors.New("manifest readback differs; retained artifacts require inspection")
	}
	old, entry, err := fs.ReadRegular(preimageName, aliasManifestLimit)
	if err != nil || aliasDigest(old) != report.BeforeSHA256 || entry.Device != plan.entry.Device || entry.Inode != plan.entry.Inode {
		report.State = "UNKNOWN"
		return report, errors.New("displaced preimage differs; retained artifacts require inspection")
	}
	report.State = "APPLIED"
	return report, nil
}

func sameAliasPlan(a, b aliasPlan) bool {
	if a.report.BeforeSHA256 != b.report.BeforeSHA256 || a.report.AfterSHA256 != b.report.AfterSHA256 || a.entry != b.entry || !reflect.DeepEqual(a.report.Removals, b.report.Removals) || len(a.identities) != len(b.identities) {
		return false
	}
	for path, info := range a.identities {
		other, ok := b.identities[path]
		if !ok || !os.SameFile(info, other) || info.Mode() != other.Mode() || info.Size() != other.Size() || info.ModTime() != other.ModTime() {
			return false
		}
	}
	return true
}

func planAliasRemoval(ctx context.Context, fs *harnessfs.Root, req AliasReconcileRequest) (aliasPlan, error) {
	plan := aliasPlan{identities: map[string]os.FileInfo{}}
	raw, entry, err := fs.ReadRegular("manifest.json", aliasManifestLimit)
	if err != nil {
		return plan, err
	}
	if entry.Mode&(0o022|0o7000) != 0 {
		return plan, errors.New("manifest must not be group/world writable")
	}
	if err = ownedAliasManifest(filepath.Join(req.Root, "workspace", "manifest.json")); err != nil {
		return plan, err
	}
	digest := aliasDigest(raw)
	if req.ExpectedSHA256 != "" && req.ExpectedSHA256 != digest {
		return plan, errors.New("manifest expected_sha256 mismatch")
	}
	if err = uniqueAliasJSON(raw); err != nil {
		return plan, err
	}
	var document map[string]json.RawMessage
	if err = json.Unmarshal(raw, &document); err != nil {
		return plan, err
	}
	var version int
	if json.Unmarshal(document["version"], &version) != nil || version != 1 {
		return plan, errors.New("unsupported workspace manifest version")
	}
	var rows []json.RawMessage
	if json.Unmarshal(document["repos"], &rows) != nil || rows == nil || len(rows) > 5000 {
		return plan, errors.New("invalid or oversized repository inventory")
	}
	type rowInfo struct{ name, lifecycle, path string }
	infos := make([]rowInfo, len(rows))
	indexes := map[string]int{}
	for i, row := range rows {
		var fields map[string]json.RawMessage
		if json.Unmarshal(row, &fields) != nil || fields == nil {
			return plan, errors.New("invalid repository row")
		}
		var name, lifecycle, path string
		if json.Unmarshal(fields["name"], &name) != nil || (!aliasNamePattern.MatchString(name) || name == "." || name == "..") {
			return plan, errors.New("invalid repository name")
		}
		if _, ok := indexes[name]; ok {
			return plan, errors.New("duplicate repository name")
		}
		indexes[name] = i
		if value, ok := fields["lifecycle"]; ok {
			if json.Unmarshal(value, &lifecycle) != nil {
				return plan, errors.New("invalid lifecycle")
			}
		}
		if value, ok := fields["path"]; ok {
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &path) != nil {
				return plan, errors.New("invalid repository path")
			}
		}
		infos[i] = rowInfo{name, lifecycle, path}
	}
	removals := []AliasRemoval{}
	removed := map[int]bool{}
	for _, name := range req.Aliases {
		index, ok := indexes[name]
		if !ok || infos[index].lifecycle != "active" || infos[index].path != "" {
			return plan, errors.New("alias must name one active default-path row")
		}
		aliasPath := filepath.Join(req.Root, name)
		aliasInfo, err := os.Lstat(aliasPath)
		if err != nil || aliasInfo.Mode()&os.ModeSymlink == 0 {
			return plan, errors.New("alias must be an existing symlink")
		}
		link, err := os.Readlink(aliasPath)
		if err != nil {
			return plan, err
		}
		target, err := filepath.EvalSymlinks(aliasPath)
		if err != nil {
			return plan, errors.New("alias target unavailable")
		}
		relative, err := filepath.Rel(req.Root, target)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return plan, errors.New("alias target escapes workspace")
		}
		canonical := ""
		for _, candidate := range infos {
			if candidate.name == name || candidate.lifecycle != "active" {
				continue
			}
			candidatePath := RepoPath(req.Root, Repo{Name: candidate.name, Path: candidate.path})
			if filepath.Clean(candidatePath) != target {
				continue
			}
			info, err := os.Lstat(candidatePath)
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			physical, err := filepath.EvalSymlinks(candidatePath)
			if err != nil || physical != candidatePath {
				continue
			}
			if canonical != "" {
				return plan, errors.New("alias has multiple active physical owners")
			}
			canonical = candidate.name
			plan.identities[candidatePath] = info
		}
		if canonical == "" {
			return plan, errors.New("alias lacks one active physical canonical row")
		}
		if err = verifyAliasGitRoot(ctx, target); err != nil {
			return plan, err
		}
		plan.identities[aliasPath] = aliasInfo
		removals = append(removals, AliasRemoval{Alias: name, Canonical: canonical, LinkTarget: link, TargetPath: target})
		removed[index] = true
	}
	kept := make([]json.RawMessage, 0, len(rows)-len(removed))
	for i, row := range rows {
		if !removed[i] {
			kept = append(kept, row)
		}
	}
	document["repos"], err = json.Marshal(kept)
	if err != nil {
		return plan, err
	}
	after, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return plan, err
	}
	after = append(after, '\n')
	if len(after) > aliasManifestLimit {
		return plan, errors.New("output manifest exceeds bound")
	}
	again, againEntry, err := fs.ReadRegular("manifest.json", aliasManifestLimit)
	if err != nil || !bytes.Equal(raw, again) || entry != againEntry {
		return plan, errors.New("manifest changed during observation")
	}
	for path, info := range plan.identities {
		current, err := os.Lstat(path)
		if err != nil || !os.SameFile(info, current) || info.Mode() != current.Mode() || info.Size() != current.Size() || info.ModTime() != current.ModTime() {
			return plan, errors.New("alias mapping changed during observation")
		}
	}
	plan.before, plan.after, plan.entry = raw, after, entry
	plan.report = AliasReconcileReport{Schema: "workspace-alias-reconcile/v1", State: "PLANNED", Root: req.Root, BeforeSHA256: digest, AfterSHA256: aliasDigest(after), Removals: removals, ManifestMode: fmt.Sprintf("%04o", entry.Mode&0o777)}
	return plan, nil
}

type aliasGitOutput struct {
	buffer   bytes.Buffer
	exceeded bool
}

func (output *aliasGitOutput) Write(data []byte) (int, error) {
	remaining := 8192 - output.buffer.Len()
	if len(data) > remaining {
		output.exceeded = true
		if remaining > 0 {
			_, _ = output.buffer.Write(data[:remaining])
		}
		return remaining, errors.New("Git identity output exceeds bound")
	}
	return output.buffer.Write(data)
}

func verifyAliasGitRoot(ctx context.Context, path string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "--show-toplevel")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	var output aliasGitOutput
	command.Stdout = &output
	command.Stderr = io.Discard
	command.WaitDelay = 100 * time.Millisecond
	err := command.Run()
	if err != nil || output.exceeded || strings.TrimSpace(output.buffer.String()) != path {
		return errors.New("canonical owner is not an independently verified Git checkout")
	}
	return nil
}

func uniqueAliasJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var scan func(int) error
	scan = func(depth int) error {
		if depth > 64 {
			return errors.New("manifest nesting exceeds bound")
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return errors.New("duplicate or invalid JSON member")
				}
				seen[name] = true
				if err = scan(depth + 1); err != nil {
					return err
				}
			}
			closing, err := decoder.Token()
			if err != nil || closing != json.Delim('}') {
				return errors.New("invalid JSON object")
			}
		case '[':
			for decoder.More() {
				if err := scan(depth + 1); err != nil {
					return err
				}
			}
			closing, err := decoder.Token()
			if err != nil || closing != json.Delim(']') {
				return errors.New("invalid JSON array")
			}
		default:
			return errors.New("invalid JSON delimiter")
		}
		return nil
	}
	if err := scan(0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("trailing JSON value")
	}
	return nil
}
