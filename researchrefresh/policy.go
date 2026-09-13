package researchrefresh

import (
	"fmt"
	"net/url"
	"path"
	"slices"
	"strings"
)

// upstreamPolicy is deliberately independent of the on-disk registry. The
// registry selects cadence and representation details, but it cannot expand
// the set of network locations the refresh package is willing to contact.
type upstreamPolicy struct {
	kind            FeedKind
	redirectTargets []string
}

var curatedUpstreams = map[string]upstreamPolicy{
	"https://api.github.com/repos/modelcontextprotocol/go-sdk/releases/latest": {
		kind: KindGitHubLatestRelease,
	},
	"https://raw.githubusercontent.com/modelcontextprotocol/go-sdk/main/docs/protocol.md": {
		kind: KindMarkdown,
	},
	"https://raw.githubusercontent.com/modelcontextprotocol/go-sdk/main/README.md": {
		kind: KindMarkdown,
	},
	"https://api.github.com/repos/modelcontextprotocol/modelcontextprotocol/releases/latest": {
		kind: KindGitHubLatestRelease,
	},
	"https://raw.githubusercontent.com/modelcontextprotocol/modelcontextprotocol/2026-07-28/schema/2026-07-28/schema.json": {
		kind: KindText,
	},
	"https://learn.chatgpt.com/docs/app-server.md": {
		kind: KindMarkdown,
	},
	"https://learn.chatgpt.com/docs/changelog": {
		kind: KindHTML,
	},
	"https://learn.chatgpt.com/docs/config-file/config-reference.md": {
		kind: KindMarkdown,
	},
	"https://learn.chatgpt.com/docs/feature-maturity.md": {
		kind: KindMarkdown,
	},
	"https://learn.chatgpt.com/docs/hooks.md": {
		kind: KindMarkdown,
	},
	"https://api.github.com/repos/openai/codex/releases/latest": {
		kind: KindGitHubLatestRelease,
	},
	"https://developers.openai.com/codex/codex-manual.md": {
		kind: KindMarkdown,
		redirectTargets: []string{
			"https://learn.chatgpt.com/docs/codex-manual.md",
		},
	},
	"https://learn.chatgpt.com/docs/models.md": {
		kind: KindMarkdown,
	},
	"https://learn.chatgpt.com/docs/codex-sdk.md": {
		kind: KindMarkdown,
	},
	"https://learn.chatgpt.com/docs/agent-configuration/subagents.md": {
		kind: KindMarkdown,
	},
}

var curatedGitHubReleasePrefixes = []string{
	"/openai/codex/releases/tag/",
	"/modelcontextprotocol/go-sdk/releases/tag/",
	"/modelcontextprotocol/modelcontextprotocol/releases/tag/",
}

func validateCuratedFeedURL(raw string, kind FeedKind) error {
	if _, err := validateHTTPSURL(raw, false); err != nil {
		return err
	}
	policy, ok := curatedUpstreams[raw]
	if !ok {
		return fmt.Errorf("%w: off-policy official upstream", ErrUnsafeURL)
	}
	if policy.kind != kind {
		return fmt.Errorf("%w: upstream does not allow kind %q", ErrUnsafeURL, kind)
	}
	return nil
}

func validateCuratedReceiptURLs(source, final string) error {
	if _, err := validateHTTPSURL(source, false); err != nil {
		return fmt.Errorf("source: %w", err)
	}
	policy, ok := curatedUpstreams[source]
	if !ok {
		return fmt.Errorf("source: %w: off-policy official upstream", ErrUnsafeURL)
	}
	if _, err := validateHTTPSURL(final, false); err != nil {
		return fmt.Errorf("final: %w", err)
	}
	if final != source && !slices.Contains(policy.redirectTargets, final) {
		return fmt.Errorf("final: %w: off-policy redirect target", ErrUnsafeURL)
	}
	return nil
}

func validateConfiguredRedirect(source, domain string) error {
	policy, ok := curatedUpstreams[source]
	if !ok {
		return ErrUnsafeURL
	}
	for _, target := range policy.redirectTargets {
		targetURL, err := url.Parse(target)
		if err == nil && targetURL.Hostname() == domain {
			return nil
		}
	}
	return fmt.Errorf("%w: redirect domain is not allowed for this upstream", ErrUnsafeURL)
}

func validateRedirectTarget(source, target string) error {
	if _, err := validateHTTPSURL(target, false); err != nil {
		return err
	}
	if target == source {
		return nil
	}
	policy, ok := curatedUpstreams[source]
	if !ok || !slices.Contains(policy.redirectTargets, target) {
		return fmt.Errorf("%w: off-policy redirect target", ErrUnsafeURL)
	}
	return nil
}

func validateCuratedReleaseURL(raw string) error {
	u, err := validateHTTPSURL(raw, false)
	if err != nil {
		return err
	}
	if u.Hostname() != "github.com" || u.RawPath != "" || path.Clean(u.Path) != u.Path {
		return fmt.Errorf("%w: off-policy release URL", ErrUnsafeURL)
	}
	for _, prefix := range curatedGitHubReleasePrefixes {
		if strings.HasPrefix(u.Path, prefix) && len(u.Path) > len(prefix) {
			return nil
		}
	}
	return fmt.Errorf("%w: off-policy release URL", ErrUnsafeURL)
}
