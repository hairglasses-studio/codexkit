package harnessrender

import "testing"

func TestNormalizeRemoteIdentityEquivalentForms(t *testing.T) {
	forms := []string{
		"https://github.com/hairglasses-studio/codex-harness-staging",
		"https://github.com/hairglasses-studio/codex-harness-staging.git",
		"ssh://git@" + "github.com/hairglasses-studio/codex-harness-staging.git",
		"git@" + "github.com:hairglasses-studio/codex-harness-staging.git",
	}
	want := "github.com/hairglasses-studio/codex-harness-staging"
	for _, form := range forms {
		identity, err := normalizeRemoteIdentity(form)
		if err != nil {
			t.Fatalf("normalizeRemoteIdentity(%q) error = %v", form, err)
		}
		if identity != want {
			t.Fatalf("normalizeRemoteIdentity(%q) = %q, want %q", form, identity, want)
		}
		if !remotesEqual(forms[0], form) {
			t.Fatalf("remotesEqual(%q, %q) = false", forms[0], form)
		}
	}
}

func TestNormalizeRemoteIdentityRejectsDifferentAuthority(t *testing.T) {
	canonical := "https://github.com/hairglasses-studio/codex-harness-staging.git"
	for _, other := range []string{
		"https://mirror.example.com/hairglasses-studio/codex-harness-staging.git",
		"https://github.com/hairglasses-studio/other.git",
	} {
		if remotesEqual(canonical, other) {
			t.Fatalf("remotesEqual(%q, %q) = true", canonical, other)
		}
	}
	for _, invalid := range []string{
		"file:///tmp/hairglasses-studio/codex-harness-staging",
		"https://github.com:443/hairglasses-studio/codex-harness-staging.git",
		"https://github.com/hairglasses-studio/codex-harness-staging.git?token=x",
		"https://github.com/hairglasses-studio/%63odex-harness-staging.git",
	} {
		if _, err := normalizeRemoteIdentity(invalid); err == nil {
			t.Fatalf("normalizeRemoteIdentity(%q) unexpectedly succeeded", invalid)
		}
	}
}
