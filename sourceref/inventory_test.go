package sourceref

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 8, 11, 20, 0, 0, 0, time.UTC)

const (
	testSHA1 = "1111111111111111111111111111111111111111"
	testSHA2 = "2222222222222222222222222222222222222222"
)

func validInventory() Inventory {
	return Inventory{
		SchemaVersion: SchemaVersion,
		Repositories: []RepositoryEvidence{
			{
				Repository:         "hairglasses-studio/zeta",
				RemoteURL:          "git@" + "github.com:hairglasses-studio/zeta.git",
				RequestedRef:       "refs/heads/main",
				RequestedSHA:       testSHA1,
				TestedRef:          "refs/heads/main",
				TestedSHA:          testSHA1,
				Checkout:           &Checkout{Path: "hairglasses-studio/zeta", Dirty: true},
				ObservedAt:         testNow.Add(-time.Hour).In(time.FixedZone("PDT", -7*60*60)),
				Lifecycle:          LifecycleActive,
				OwnershipClass:     OwnershipCodexHarnessSpecific,
				EvidenceClass:      EvidenceRemoteTree,
				ObservationCommand: "git ls-tree refs/remotes/origin/main",
			},
			{
				Repository:         "hairglasses-studio/alpha",
				RemoteURL:          "https://github.com/hairglasses-studio/alpha.git",
				RequestedRef:       "main",
				RequestedSHA:       testSHA1,
				TestedRef:          testSHA1,
				TestedSHA:          testSHA1,
				Checkout:           &Checkout{Path: "hairglasses-studio/worktrees/alpha", Dirty: false},
				ObservedAt:         testNow.Add(-2 * time.Hour),
				Lifecycle:          LifecycleMaintenanceOnly,
				OwnershipClass:     OwnershipRepoLocalProjection,
				EvidenceClass:      EvidenceFreshWorktree,
				ObservationCommand: "git -C hairglasses-studio/worktrees/alpha rev-parse HEAD",
			},
			{
				Repository:         "hairglasses-studio/beta",
				RemoteURL:          "ssh://git@" + "github.com/hairglasses-studio/beta.git",
				RequestedRef:       "origin/main",
				RequestedSHA:       testSHA1,
				TestedRef:          "refs/heads/task",
				TestedSHA:          testSHA2,
				Checkout:           &Checkout{Path: "hairglasses-studio/worktrees/beta", Dirty: true},
				ObservedAt:         testNow.Add(-3 * time.Hour),
				Lifecycle:          LifecycleNeverPublish,
				OwnershipClass:     OwnershipExternalOwner,
				EvidenceClass:      EvidencePrimaryCheckout,
				ObservationCommand: "git -C hairglasses-studio/worktrees/beta status --porcelain",
			},
			{
				Repository:         "hairglasses-studio/glass",
				TestedRef:          "HEAD",
				TestedSHA:          testSHA2,
				Checkout:           &Checkout{Path: "hairglasses-studio/glass", Dirty: true},
				ObservedAt:         testNow.Add(-5 * time.Hour),
				Lifecycle:          LifecycleDetached,
				OwnershipClass:     OwnershipRepoLocalProjection,
				EvidenceClass:      EvidenceLocalOnly,
				ObservationCommand: "git -C hairglasses-studio/glass rev-parse HEAD",
			},
			{
				Repository:         "hairglasses-studio/runtime",
				RemoteURL:          "https://github.com/hairglasses-studio/runtime",
				RequestedRef:       "refs/tags/v1.0.0",
				RequestedSHA:       testSHA2,
				TestedRef:          "refs/tags/v1.0.0",
				TestedSHA:          testSHA2,
				Checkout:           &Checkout{Path: "runtime-checkouts/hairglasses-runtime", Dirty: false},
				ObservedAt:         testNow.Add(-4 * time.Hour),
				Lifecycle:          LifecycleArchived,
				OwnershipClass:     OwnershipSharedProviderNeutral,
				EvidenceClass:      EvidenceLiveRuntime,
				ObservationCommand: "git -C runtime-checkouts/hairglasses-runtime rev-parse HEAD",
			},
		},
	}
}

func testOptions() Options { return Options{Now: testNow, MaxAge: 12 * time.Hour} }

func TestValidateAcceptsAllEvidenceClassesAndRemoteForms(t *testing.T) {
	if err := Validate(validInventory(), testOptions()); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateAcceptsEveryClosedLifecycleAndOwnershipClass(t *testing.T) {
	lifecycles := []Lifecycle{
		LifecycleActive,
		LifecycleMaintenanceOnly,
		LifecycleNeverPublish,
		LifecyclePublishMirror,
		LifecycleArchived,
		LifecycleDeprecated,
		LifecycleCompatibility,
		LifecycleDetached,
		LifecycleReferenceOnly,
	}
	ownershipClasses := []OwnershipClass{
		OwnershipSharedProviderNeutral,
		OwnershipCodexHarnessSpecific,
		OwnershipClaudeHarnessSpecific,
		OwnershipRepoLocalProjection,
		OwnershipExternalOwner,
	}
	for _, lifecycle := range lifecycles {
		t.Run("lifecycle_"+string(lifecycle), func(t *testing.T) {
			inventory := validInventory()
			inventory.Repositories = inventory.Repositories[:1]
			inventory.Repositories[0].Lifecycle = lifecycle
			if err := Validate(inventory, testOptions()); err != nil {
				t.Fatalf("Validate() lifecycle %q error = %v", lifecycle, err)
			}
		})
	}
	for _, ownershipClass := range ownershipClasses {
		t.Run("ownership_"+string(ownershipClass), func(t *testing.T) {
			inventory := validInventory()
			inventory.Repositories = inventory.Repositories[:1]
			inventory.Repositories[0].OwnershipClass = ownershipClass
			if err := Validate(inventory, testOptions()); err != nil {
				t.Fatalf("Validate() ownership class %q error = %v", ownershipClass, err)
			}
		})
	}
}

func TestValidateAcceptsGitHubCommunityRepositoryIdentity(t *testing.T) {
	inventory := validInventory()
	inventory.Repositories = inventory.Repositories[:1]
	inventory.Repositories[0].Repository = "hairglasses-studio/.github"
	inventory.Repositories[0].RemoteURL = "git@" + "github.com:hairglasses-studio/.github.git"
	inventory.Repositories[0].Checkout.Path = "hairglasses-studio/.github"
	if err := Validate(inventory, testOptions()); err != nil {
		t.Fatalf("Validate() .github repository error = %v", err)
	}
}

func TestLocalOnlyEvidenceOmitsRemoteClaimsAndAllowsMultipleRepositories(t *testing.T) {
	inventory := validInventory()
	glass := inventory.Repositories[3]
	vault := glass
	vault.Repository = "hairglasses-studio/vault"
	vault.Checkout = &Checkout{Path: "hairglasses-studio/vault", Dirty: false}
	vault.Lifecycle = LifecycleReferenceOnly
	vault.ObservationCommand = "git -C hairglasses-studio/vault rev-parse HEAD"
	inventory.Repositories = []RepositoryEvidence{vault, glass}

	data, err := Marshal(inventory, testOptions())
	if err != nil {
		t.Fatalf("Marshal() local-only error = %v", err)
	}
	for _, field := range []string{"remote_url", "requested_ref", "requested_sha"} {
		if strings.Contains(string(data), `"`+field+`"`) {
			t.Fatalf("Marshal() emitted %s for local-only evidence:\n%s", field, data)
		}
	}
	loaded, err := Load(data, testOptions())
	if err != nil {
		t.Fatalf("Load() local-only error = %v", err)
	}
	if len(loaded.Repositories) != 2 || loaded.Repositories[0].Repository != "hairglasses-studio/glass" || loaded.Repositories[1].Repository != "hairglasses-studio/vault" {
		t.Fatalf("Load() local-only repositories = %#v", loaded.Repositories)
	}
}

func TestMarshalIsDeterministicCanonicalAndDoesNotMutate(t *testing.T) {
	inventory := validInventory()
	originalFirst := inventory.Repositories[0]

	first, err := Marshal(inventory, testOptions())
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	second, err := Marshal(inventory, testOptions())
	if err != nil {
		t.Fatalf("Marshal() second error = %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("Marshal() is not deterministic:\n%s\n%s", first, second)
	}
	if first[len(first)-1] != '\n' {
		t.Fatal("Marshal() did not append a trailing newline")
	}
	alpha := strings.Index(string(first), `"repository": "hairglasses-studio/alpha"`)
	zeta := strings.Index(string(first), `"repository": "hairglasses-studio/zeta"`)
	if alpha < 0 || zeta < 0 || alpha >= zeta {
		t.Fatalf("Marshal() did not sort repositories:\n%s", first)
	}
	if !strings.Contains(string(first), `"observed_at": "2026-08-11T19:00:00Z"`) {
		t.Fatalf("Marshal() did not canonicalize observed_at to UTC:\n%s", first)
	}
	if inventory.Repositories[0].Repository != originalFirst.Repository ||
		!inventory.Repositories[0].ObservedAt.Equal(originalFirst.ObservedAt) ||
		inventory.Repositories[0].ObservedAt.Location() != originalFirst.ObservedAt.Location() {
		t.Fatal("Marshal() mutated its caller")
	}

	loaded, err := Load(first, testOptions())
	if err != nil {
		t.Fatalf("Load(Marshal()) error = %v", err)
	}
	if got := loaded.Repositories[0].Repository; got != "hairglasses-studio/alpha" {
		t.Fatalf("loaded first repository = %q", got)
	}
}

func TestValidateRejectsInvalidInventoryFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Inventory, *Options)
		want   string
	}{
		{name: "schema", mutate: func(in *Inventory, _ *Options) { in.SchemaVersion = 2 }, want: "schema_version"},
		{name: "nil repositories", mutate: func(in *Inventory, _ *Options) { in.Repositories = nil }, want: "non-empty array"},
		{name: "empty repositories", mutate: func(in *Inventory, _ *Options) { in.Repositories = []RepositoryEvidence{} }, want: "non-empty array"},
		{name: "duplicate repository case folded", mutate: func(in *Inventory, _ *Options) {
			duplicate := in.Repositories[0]
			duplicate.Repository = "Hairglasses-Studio/ZETA"
			duplicate.RemoteURL = "https://github.com/Hairglasses-Studio/ZETA.git"
			in.Repositories = append(in.Repositories, duplicate)
		}, want: "duplicate repository"},
		{name: "duplicate checkout", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[2].Checkout.Path = in.Repositories[1].Checkout.Path
		}, want: "same checkout path"},
		{name: "bad repository identity", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[0].Repository = "zeta"
		}, want: "namespace and name"},
		{name: "remote identity mismatch", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[0].RemoteURL = "https://github.com/hairglasses-studio/other.git"
		}, want: "does not match identity"},
		{name: "unsafe remote scheme", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[0].RemoteURL = "file:///tmp/hairglasses-studio/zeta"
		}, want: "https or ssh"},
		{name: "unsafe remote host", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[0].RemoteURL = "https://localhost/hairglasses-studio/zeta.git"
		}, want: "unsafe remote_url host"},
		{name: "remote query", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[0].RemoteURL = "https://github.com/hairglasses-studio/zeta.git?token=no"
		}, want: "query"},
		{name: "remote user info", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[0].RemoteURL = "https://me@" + "github.com/hairglasses-studio/zeta.git"
		}, want: "user information"},
		{name: "remote port", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[0].RemoteURL = "https://github.com:443/hairglasses-studio/zeta.git"
		}, want: "explicit port"},
		{name: "remote-backed missing remote", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[0].RemoteURL = ""
		}, want: "remote_url"},
		{name: "remote-backed missing requested ref", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[0].RequestedRef = ""
		}, want: "requested_ref"},
		{name: "remote-backed missing requested sha", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[0].RequestedSHA = ""
		}, want: "requested_sha"},
		{name: "bad requested ref", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[0].RequestedRef = "refs/heads/bad..ref"
		}, want: "requested_ref"},
		{name: "bad tested ref", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[0].TestedRef = "refs/heads/bad ref"
		}, want: "tested_ref"},
		{name: "short sha", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[0].RequestedSHA = "abc123"
		}, want: "requested_sha"},
		{name: "uppercase sha", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[0].TestedSHA = strings.Repeat("A", 40)
		}, want: "tested_sha"},
		{name: "zero sha", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[0].RequestedSHA = strings.Repeat("0", 40)
		}, want: "requested_sha"},
		{name: "local only claims remote", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[3].RemoteURL = "https://github.com/hairglasses-studio/glass.git"
		}, want: "must not claim"},
		{name: "local only claims requested ref", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[3].RequestedRef = "main"
		}, want: "must not claim"},
		{name: "local only claims requested sha", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[3].RequestedSHA = testSHA1
		}, want: "must not claim"},
		{name: "local only missing tested ref", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[3].TestedRef = ""
		}, want: "tested_ref"},
		{name: "local only missing tested sha", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[3].TestedSHA = ""
		}, want: "tested_sha"},
		{name: "same ref different shas", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[2].TestedRef = in.Repositories[2].RequestedRef
		}, want: "same requested and tested ref"},
		{name: "zero observed", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[0].ObservedAt = time.Time{}
		}, want: "observed_at is required"},
		{name: "future", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[0].ObservedAt = testNow.Add(time.Nanosecond)
		}, want: "future"},
		{name: "stale", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[0].ObservedAt = testNow.Add(-13 * time.Hour)
		}, want: "stale"},
		{name: "unknown lifecycle", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[0].Lifecycle = "forgotten"
		}, want: "unknown lifecycle"},
		{name: "unknown ownership", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[0].OwnershipClass = "shared"
		}, want: "unknown ownership_class"},
		{name: "unknown evidence", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[0].EvidenceClass = "guess"
		}, want: "unknown evidence_class"},
		{name: "empty command", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[0].ObservationCommand = ""
		}, want: "observation_command"},
		{name: "multiline command", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[0].ObservationCommand = "git status\nwhoami"
		}, want: "observation_command"},
		{name: "absolute checkout", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[1].Checkout.Path = "/opt/hg/hairglasses-studio/alpha"
		}, want: "workspace-relative"},
		{name: "single checkout component", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[1].Checkout.Path = "alpha"
		}, want: "workspace and repository"},
		{name: "unclean checkout", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[1].Checkout.Path = "hairglasses-studio/worktrees/../alpha"
		}, want: "workspace-relative"},
		{name: "traversing checkout", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[1].Checkout.Path = "../hairglasses-studio/alpha"
		}, want: "workspace-relative"},
		{name: "root checkout", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[1].Checkout.Path = "/"
		}, want: "workspace-relative"},
		{name: "absolute path command", mutate: func(in *Inventory, _ *Options) {
			in.Repositories[0].ObservationCommand = "git -C /opt/hg/hairglasses-studio/zeta rev-parse HEAD"
		}, want: "absolute path"},
		{name: "missing validation clock", mutate: func(_ *Inventory, opts *Options) {
			opts.Now = time.Time{}
		}, want: "validation time is required"},
		{name: "negative max age", mutate: func(_ *Inventory, opts *Options) {
			opts.MaxAge = -time.Second
		}, want: "max age"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inventory := validInventory()
			opts := testOptions()
			test.mutate(&inventory, &opts)
			err := Validate(inventory, opts)
			if err == nil || !errors.Is(err, ErrInvalidInventory) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want ErrInvalidInventory containing %q", err, test.want)
			}
		})
	}
}

func TestValidateRejectsIncoherentEvidenceClasses(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RepositoryEvidence)
		want   string
	}{
		{name: "missing checkout", mutate: func(row *RepositoryEvidence) {
			row.Checkout = nil
		}, want: "checkout state is required"},
		{name: "remote tree sha mismatch", mutate: func(row *RepositoryEvidence) {
			row.TestedRef = "refs/heads/task"
			row.TestedSHA = testSHA2
		}, want: "requested_sha and tested_sha must match"},
		{name: "fresh worktree missing checkout", mutate: func(row *RepositoryEvidence) {
			row.EvidenceClass = EvidenceFreshWorktree
			row.Checkout = nil
		}, want: "checkout state is required"},
		{name: "fresh worktree dirty", mutate: func(row *RepositoryEvidence) {
			row.EvidenceClass = EvidenceFreshWorktree
			row.Checkout = &Checkout{Path: "hairglasses-studio/zeta", Dirty: true}
		}, want: "cannot be dirty"},
		{name: "fresh worktree sha mismatch", mutate: func(row *RepositoryEvidence) {
			row.EvidenceClass = EvidenceFreshWorktree
			row.Checkout = &Checkout{Path: "hairglasses-studio/zeta"}
			row.TestedRef = "refs/heads/task"
			row.TestedSHA = testSHA2
		}, want: "requested_sha and tested_sha must match"},
		{name: "primary checkout missing", mutate: func(row *RepositoryEvidence) {
			row.EvidenceClass = EvidencePrimaryCheckout
			row.Checkout = nil
		}, want: "checkout state is required"},
		{name: "live runtime missing", mutate: func(row *RepositoryEvidence) {
			row.EvidenceClass = EvidenceLiveRuntime
			row.Checkout = nil
		}, want: "checkout state is required"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inventory := validInventory()
			inventory.Repositories = inventory.Repositories[:1]
			test.mutate(&inventory.Repositories[0])
			err := Validate(inventory, testOptions())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestLoadRejectsNonExactJSON(t *testing.T) {
	valid, err := Marshal(validInventory(), testOptions())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{name: "empty", data: nil, want: "document size"},
		{name: "too large", data: bytes.Repeat([]byte{' '}, MaxDocumentBytes+1), want: "document size"},
		{name: "unknown top-level field", data: bytes.Replace(valid, []byte(`"schema_version": 1`), []byte(`"schema_version": 1, "extra": true`), 1), want: "unknown field"},
		{name: "unknown nested field", data: bytes.Replace(valid, []byte(`"dirty": false`), []byte(`"dirty": false, "extra": true`), 1), want: "unknown field"},
		{name: "duplicate key", data: bytes.Replace(valid, []byte(`"schema_version": 1`), []byte(`"schema_version": 1, "schema_version": 1`), 1), want: "duplicate object key"},
		{name: "duplicate nested key", data: bytes.Replace(valid, []byte(`"dirty": false`), []byte(`"dirty": false, "dirty": false`), 1), want: "duplicate object key"},
		{name: "trailing document", data: append(slicesClone(valid), []byte(` {}`)...), want: "trailing JSON value"},
		{name: "malformed", data: []byte(`{"schema_version":`), want: "decode"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Load(test.data, testOptions())
			if err == nil || !errors.Is(err, ErrInvalidInventory) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load() error = %v, want ErrInvalidInventory containing %q", err, test.want)
			}
		})
	}
}

func TestDefaultMaxAge(t *testing.T) {
	inventory := validInventory()
	inventory.Repositories = inventory.Repositories[:1]
	inventory.Repositories[0].ObservedAt = testNow.Add(-DefaultMaxAge)
	if err := Validate(inventory, Options{Now: testNow}); err != nil {
		t.Fatalf("Validate() at default age boundary error = %v", err)
	}
	inventory.Repositories[0].ObservedAt = inventory.Repositories[0].ObservedAt.Add(-time.Nanosecond)
	if err := Validate(inventory, Options{Now: testNow}); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("Validate() beyond default age error = %v", err)
	}
}

func slicesClone(value []byte) []byte {
	return append([]byte(nil), value...)
}
