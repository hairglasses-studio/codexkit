package launchercontract

import (
	"encoding/json"
	"errors"
	"path"
	"strings"
	"testing"
)

const (
	testCommit  = "1234567890abcdef1234567890abcdef12345678"
	testDigest  = "sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	testDigest2 = "sha256:abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"
)

func validTrust() Trust {
	return Trust{
		Repository:       CanonicalRepository,
		NormalizedRemote: CanonicalRemote,
		ExpectedSHA:      testCommit,
		RequiredMarkers: []MarkerTrust{
			{ID: "harness-agents", RelativePath: "AGENTS.md", SHA256: testDigest},
			{ID: "codexkit-module", RelativePath: "codexkit/go.mod", SHA256: testDigest2},
		},
	}
}

func validObservations(candidate string) Observations {
	return Observations{
		Candidate:     candidate,
		GitTopLevel:   candidate,
		GitCommonDir:  "/srv/git/codex-harness-staging/.git",
		Repository:    CanonicalRepository,
		RemoteURL:     "git@" + "github.com:hairglasses-studio/codex-harness-staging.git",
		TestedSHA:     testCommit,
		CheckoutClean: true,
		Markers: []MarkerObservation{
			{ID: "codexkit-module", RelativePath: "codexkit/go.mod", SHA256: testDigest2, Contained: true, Regular: true},
			{ID: "harness-agents", RelativePath: "AGENTS.md", SHA256: testDigest, Contained: true, Regular: true},
		},
	}
}

func cloneObservations(observations Observations) Observations {
	observations.Markers = append([]MarkerObservation(nil), observations.Markers...)
	return observations
}

func cloneTrust(trust Trust) Trust {
	trust.RequiredMarkers = append([]MarkerTrust(nil), trust.RequiredMarkers...)
	return trust
}

func TestResolvePrecedenceAndCheckoutModes(t *testing.T) {
	t.Parallel()
	candidate := "/opt/test/Harness With Spaces"
	baseObservations := validObservations(candidate)
	trust := validTrust()
	tests := []struct {
		name       string
		inputs     Inputs
		mutate     func(*Observations)
		wantSource Source
		wantMode   Mode
	}{
		{
			name: "explicit harness wins with coherent legacy",
			inputs: Inputs{
				CodexHarnessDir:    InputValue{Present: true, Value: candidate},
				LegacyCodexkitRoot: InputValue{Present: true, Value: path.Join(candidate, "codexkit")},
				LauncherDefault:    InputValue{Present: true, Value: "relative ignored default"},
			},
			wantSource: SourceCodexHarnessDir,
			wantMode:   ModeLinkedWorktree,
		},
		{
			name: "legacy derives central harness root",
			inputs: Inputs{
				LegacyCodexkitRoot: InputValue{Present: true, Value: path.Join(candidate, "codexkit")},
				LauncherDefault:    InputValue{Present: true, Value: "/ignored/default"},
			},
			wantSource: SourceLegacyCodexkitRoot,
			wantMode:   ModeLinkedWorktree,
		},
		{
			name: "launcher default",
			inputs: Inputs{
				LauncherDefault: InputValue{Present: true, Value: candidate},
			},
			wantSource: SourceLauncherDefault,
			wantMode:   ModeLinkedWorktree,
		},
		{
			name: "primary checkout identity",
			inputs: Inputs{
				CodexHarnessDir: InputValue{Present: true, Value: candidate},
			},
			mutate: func(observations *Observations) {
				observations.GitCommonDir = path.Join(candidate, ".git")
			},
			wantSource: SourceCodexHarnessDir,
			wantMode:   ModePrimaryCheckout,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			observations := cloneObservations(baseObservations)
			if test.mutate != nil {
				test.mutate(&observations)
			}
			resolution, err := Resolve(test.inputs, observations, trust)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if resolution.Source != test.wantSource || resolution.Mode != test.wantMode {
				t.Fatalf("Resolve() source/mode = %q/%q, want %q/%q", resolution.Source, resolution.Mode, test.wantSource, test.wantMode)
			}
			if resolution.Candidate != candidate || resolution.GitTopLevel != candidate || resolution.CodexkitRoot != path.Join(candidate, "codexkit") {
				t.Fatalf("Resolve() paths = %#v", resolution)
			}
			if !resolution.ActivationEligible || !resolution.CheckoutClean || resolution.Repository != CanonicalRepository || resolution.NormalizedRemote != CanonicalRemote {
				t.Fatalf("Resolve() authority = %#v", resolution)
			}
			if got := resolution.Markers[0].ID; got != "codexkit-module" {
				t.Fatalf("Resolve() did not canonicalize markers: %#v", resolution.Markers)
			}
		})
	}
}

func TestResolveAcceptsNormalizedRemoteForms(t *testing.T) {
	t.Parallel()
	candidate := "/work/harness"
	inputs := Inputs{CodexHarnessDir: InputValue{Present: true, Value: candidate}}
	for _, remote := range []string{
		"git@" + "github.com:hairglasses-studio/codex-harness-staging.git",
		"https://github.com/hairglasses-studio/codex-harness-staging",
		"https://github.com/hairglasses-studio/codex-harness-staging.git",
		"ssh://git@" + "github.com/hairglasses-studio/codex-harness-staging.git",
	} {
		remote := remote
		t.Run(remote, func(t *testing.T) {
			t.Parallel()
			observations := validObservations(candidate)
			observations.RemoteURL = remote
			resolution, err := Resolve(inputs, observations, validTrust())
			if err != nil {
				t.Fatalf("Resolve(%q) error = %v", remote, err)
			}
			if resolution.NormalizedRemote != CanonicalRemote {
				t.Fatalf("normalized remote = %q", resolution.NormalizedRemote)
			}
		})
	}
}

func TestResolveRejectsInputMatrixWithoutFallback(t *testing.T) {
	t.Parallel()
	candidate := "/work/harness"
	validLegacy := InputValue{Present: true, Value: path.Join(candidate, "codexkit")}
	validDefault := InputValue{Present: true, Value: candidate}
	tests := []struct {
		name   string
		inputs Inputs
		want   string
	}{
		{name: "no candidate", inputs: Inputs{}, want: "no launcher candidate"},
		{name: "explicit empty blocks fallback", inputs: Inputs{CodexHarnessDir: InputValue{Present: true}, LegacyCodexkitRoot: validLegacy, LauncherDefault: validDefault}, want: "CODEX_HARNESS_DIR"},
		{name: "explicit relative blocks fallback", inputs: Inputs{CodexHarnessDir: InputValue{Present: true, Value: "relative"}, LegacyCodexkitRoot: validLegacy, LauncherDefault: validDefault}, want: "CODEX_HARNESS_DIR"},
		{name: "explicit unclean blocks fallback", inputs: Inputs{CodexHarnessDir: InputValue{Present: true, Value: "/work/../harness"}, LegacyCodexkitRoot: validLegacy}, want: "CODEX_HARNESS_DIR"},
		{name: "explicit root blocks fallback", inputs: Inputs{CodexHarnessDir: InputValue{Present: true, Value: "/"}, LegacyCodexkitRoot: validLegacy}, want: "CODEX_HARNESS_DIR"},
		{name: "incoherent legacy fails with explicit", inputs: Inputs{CodexHarnessDir: validDefault, LegacyCodexkitRoot: InputValue{Present: true, Value: "/other/codexkit"}}, want: "must equal"},
		{name: "legacy empty blocks default", inputs: Inputs{LegacyCodexkitRoot: InputValue{Present: true}, LauncherDefault: validDefault}, want: "CODEXKIT_ROOT"},
		{name: "legacy not codexkit blocks default", inputs: Inputs{LegacyCodexkitRoot: InputValue{Present: true, Value: "/work/standalone"}, LauncherDefault: validDefault}, want: "harness/codexkit"},
		{name: "legacy relative blocks default", inputs: Inputs{LegacyCodexkitRoot: InputValue{Present: true, Value: "codexkit"}, LauncherDefault: validDefault}, want: "CODEXKIT_ROOT"},
		{name: "default relative", inputs: Inputs{LauncherDefault: InputValue{Present: true, Value: "relative"}}, want: "launcher default"},
		{name: "absent explicit carries value", inputs: Inputs{CodexHarnessDir: InputValue{Value: candidate}, LauncherDefault: validDefault}, want: "absent CODEX_HARNESS_DIR"},
		{name: "absent legacy carries value", inputs: Inputs{LegacyCodexkitRoot: InputValue{Value: path.Join(candidate, "codexkit")}, LauncherDefault: validDefault}, want: "absent CODEXKIT_ROOT"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := Resolve(test.inputs, validObservations(candidate), validTrust())
			if err == nil || !errors.Is(err, ErrInvalidContract) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Resolve() error = %v, want ErrInvalidContract containing %q", err, test.want)
			}
		})
	}
}

func TestResolveRejectsObservationAndTrustMatrix(t *testing.T) {
	t.Parallel()
	candidate := "/work/harness"
	inputs := Inputs{CodexHarnessDir: InputValue{Present: true, Value: candidate}}
	tests := []struct {
		name   string
		mutate func(*Observations, *Trust)
		want   string
	}{
		{name: "candidate mismatch", mutate: func(o *Observations, _ *Trust) { o.Candidate = "/work/other" }, want: "selected candidate"},
		{name: "top-level mismatch", mutate: func(o *Observations, _ *Trust) { o.GitTopLevel = "/work/other" }, want: "top-level"},
		{name: "common-dir relative", mutate: func(o *Observations, _ *Trust) { o.GitCommonDir = ".git" }, want: "common-dir"},
		{name: "common-dir equals worktree", mutate: func(o *Observations, _ *Trust) { o.GitCommonDir = o.Candidate }, want: "must not equal"},
		{name: "standalone codexkit repository", mutate: func(o *Observations, _ *Trust) {
			o.Repository = "hairglasses-studio/codexkit"
			o.RemoteURL = "git@" + "github.com:hairglasses-studio/codexkit.git"
		}, want: "not the central harness"},
		{name: "standalone codexkit remote", mutate: func(o *Observations, _ *Trust) { o.RemoteURL = "git@" + "github.com:hairglasses-studio/codexkit.git" }, want: "does not identify"},
		{name: "remote credentials", mutate: func(o *Observations, _ *Trust) {
			o.RemoteURL = "https://user:secret@" + "github.com/hairglasses-studio/codex-harness-staging.git"
		}, want: "user information"},
		{name: "remote query", mutate: func(o *Observations, _ *Trust) {
			o.RemoteURL = "https://github.com/hairglasses-studio/codex-harness-staging.git?token=x"
		}, want: "query"},
		{name: "remote port", mutate: func(o *Observations, _ *Trust) {
			o.RemoteURL = "ssh://git@" + "github.com:22/hairglasses-studio/codex-harness-staging.git"
		}, want: "port"},
		{name: "remote scheme", mutate: func(o *Observations, _ *Trust) { o.RemoteURL = "file:///work/harness" }, want: "https or ssh"},
		{name: "short SHA", mutate: func(o *Observations, _ *Trust) { o.TestedSHA = "abc123" }, want: "40-hex"},
		{name: "uppercase SHA", mutate: func(o *Observations, _ *Trust) { o.TestedSHA = strings.Repeat("A", 40) }, want: "40-hex"},
		{name: "zero SHA", mutate: func(o *Observations, _ *Trust) { o.TestedSHA = strings.Repeat("0", 40) }, want: "40-hex"},
		{name: "different valid SHA", mutate: func(o *Observations, _ *Trust) { o.TestedSHA = "abcdef1234567890abcdef1234567890abcdef12" }, want: "trusted expected SHA"},
		{name: "dirty checkout", mutate: func(o *Observations, _ *Trust) { o.CheckoutClean = false }, want: "dirty"},
		{name: "missing marker", mutate: func(o *Observations, _ *Trust) { o.Markers = o.Markers[:1] }, want: "exactly cover"},
		{name: "extra marker", mutate: func(o *Observations, _ *Trust) {
			o.Markers = append(o.Markers, MarkerObservation{ID: "extra", RelativePath: "extra", SHA256: testDigest, Contained: true, Regular: true})
		}, want: "exactly cover"},
		{name: "duplicate marker ID", mutate: func(o *Observations, _ *Trust) { o.Markers[1].ID = o.Markers[0].ID }, want: "duplicate marker"},
		{name: "marker path mismatch", mutate: func(o *Observations, _ *Trust) { o.Markers[0].RelativePath = "codexkit/go.sum" }, want: "path does not match"},
		{name: "marker escaping", mutate: func(o *Observations, _ *Trust) { o.Markers[0].RelativePath = "../go.mod" }, want: "path does not match"},
		{name: "marker uncontained", mutate: func(o *Observations, _ *Trust) { o.Markers[0].Contained = false }, want: "not observed inside"},
		{name: "marker symlink or nonregular", mutate: func(o *Observations, _ *Trust) { o.Markers[0].Regular = false }, want: "not a no-follow regular"},
		{name: "marker digest mismatch", mutate: func(o *Observations, _ *Trust) { o.Markers[0].SHA256 = testDigest }, want: "digest does not match"},
		{name: "wrong trust repository", mutate: func(_ *Observations, trust *Trust) { trust.Repository = "hairglasses-studio/codexkit" }, want: "trust repository"},
		{name: "wrong trust remote", mutate: func(_ *Observations, trust *Trust) { trust.NormalizedRemote = "github.com/hairglasses-studio/codexkit" }, want: "trust normalized"},
		{name: "missing trust expected SHA", mutate: func(_ *Observations, trust *Trust) { trust.ExpectedSHA = "" }, want: "trust expected SHA"},
		{name: "empty trust markers", mutate: func(_ *Observations, trust *Trust) { trust.RequiredMarkers = nil }, want: "required markers"},
		{name: "duplicate trust marker ID", mutate: func(_ *Observations, trust *Trust) { trust.RequiredMarkers[1].ID = trust.RequiredMarkers[0].ID }, want: "duplicate required marker ID"},
		{name: "duplicate trust marker path", mutate: func(_ *Observations, trust *Trust) {
			trust.RequiredMarkers[1].RelativePath = trust.RequiredMarkers[0].RelativePath
		}, want: "duplicate required marker path"},
		{name: "invalid trust digest", mutate: func(_ *Observations, trust *Trust) { trust.RequiredMarkers[0].SHA256 = "sha256:no" }, want: "invalid sha256"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			observations := cloneObservations(validObservations(candidate))
			trust := cloneTrust(validTrust())
			test.mutate(&observations, &trust)
			_, err := Resolve(inputs, observations, trust)
			if err == nil || !errors.Is(err, ErrInvalidContract) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Resolve() error = %v, want ErrInvalidContract containing %q", err, test.want)
			}
		})
	}
}

func TestResolutionAuthorityRejectsFabricationAndGenericJSON(t *testing.T) {
	t.Parallel()
	candidate := "/opt/alice/private harness"
	resolution, err := Resolve(
		Inputs{CodexHarnessDir: InputValue{Present: true, Value: candidate}},
		validObservations(candidate),
		validTrust(),
	)
	if err != nil {
		t.Fatal(err)
	}
	wantAuthority, err := AuthoritySHA256(validTrust())
	if err != nil {
		t.Fatal(err)
	}
	if resolution.AuthoritySHA256 != wantAuthority {
		t.Fatalf("resolution authority = %q, want %q", resolution.AuthoritySHA256, wantAuthority)
	}

	tamperedSHA := resolution
	tamperedSHA.TestedSHA = "abcdef1234567890abcdef1234567890abcdef12"
	if err := ValidateResolution(tamperedSHA); !errors.Is(err, ErrInvalidContract) || !strings.Contains(err.Error(), "authority digest") {
		t.Fatalf("ValidateResolution(tampered SHA) error = %v", err)
	}
	tamperedMarker := resolution
	tamperedMarker.Markers = append([]MarkerObservation(nil), resolution.Markers...)
	tamperedMarker.Markers[0].SHA256 = testDigest
	if err := ValidateResolution(tamperedMarker); !errors.Is(err, ErrInvalidContract) || !strings.Contains(err.Error(), "authority digest") {
		t.Fatalf("ValidateResolution(tampered marker) error = %v", err)
	}
	forgedAuthority := resolution
	forgedAuthority.AuthoritySHA256 = testDigest
	if err := ValidateResolution(forgedAuthority); !errors.Is(err, ErrInvalidContract) || !strings.Contains(err.Error(), "authority digest") {
		t.Fatalf("ValidateResolution(forged authority) error = %v", err)
	}

	data, err := json.Marshal(resolution)
	if !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("json.Marshal(Resolution) = %q, %v; want ErrInvalidContract", data, err)
	}
	if strings.Contains(string(data), candidate) {
		t.Fatalf("generic Resolution JSON leaked candidate: %s", data)
	}
}

func TestAuthoritySHA256IsCanonicalAndBindsTrust(t *testing.T) {
	t.Parallel()
	trust := validTrust()
	first, err := AuthoritySHA256(trust)
	if err != nil {
		t.Fatal(err)
	}
	reordered := cloneTrust(trust)
	reordered.RequiredMarkers[0], reordered.RequiredMarkers[1] = reordered.RequiredMarkers[1], reordered.RequiredMarkers[0]
	second, err := AuthoritySHA256(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("authority digest depends on marker order: %q != %q", first, second)
	}

	mutations := []func(*Trust){
		func(value *Trust) { value.ExpectedSHA = "abcdef1234567890abcdef1234567890abcdef12" },
		func(value *Trust) { value.RequiredMarkers[0].ID = "other-marker" },
		func(value *Trust) { value.RequiredMarkers[0].RelativePath = "OTHER.md" },
		func(value *Trust) { value.RequiredMarkers[0].SHA256 = testDigest2 },
	}
	for index, mutate := range mutations {
		changed := cloneTrust(trust)
		mutate(&changed)
		digest, digestErr := AuthoritySHA256(changed)
		if digestErr != nil {
			t.Fatalf("mutation %d: AuthoritySHA256() error = %v", index, digestErr)
		}
		if digest == first {
			t.Fatalf("mutation %d did not change authority digest", index)
		}
	}
}

func TestPathIDIsDeterministicAndSanitized(t *testing.T) {
	t.Parallel()
	privatePath := "/opt/alice/Harness With Spaces"
	first, err := PathID(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PathID(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || !validPathID(first) || strings.Contains(first, "alice") {
		t.Fatalf("PathID() = %q, %q", first, second)
	}
	for _, invalid := range []string{"", "/", "relative", "/unclean/../path", "/bad\npath", `C:\work`} {
		if _, err := PathID(invalid); !errors.Is(err, ErrInvalidContract) {
			t.Fatalf("PathID(%q) error = %v, want ErrInvalidContract", invalid, err)
		}
	}
}

func FuzzResolveNeverPanics(f *testing.F) {
	f.Add("/work/harness", "git@"+"github.com:hairglasses-studio/codex-harness-staging.git", testCommit)
	f.Add("relative", "file:///tmp/codexkit", "abc")
	f.Fuzz(func(t *testing.T, candidate, remote, commit string) {
		inputs := Inputs{CodexHarnessDir: InputValue{Present: true, Value: candidate}}
		observations := validObservations(candidate)
		observations.RemoteURL = remote
		observations.TestedSHA = commit
		_, _ = Resolve(inputs, observations, validTrust())
	})
}
