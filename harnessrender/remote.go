package harnessrender

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
)

var scpRemotePattern = regexp.MustCompile(`^git@([^/:]+):(.+)$`)

// normalizeRemoteIdentity returns the credential-free host/repository identity
// shared by HTTPS, SSH URL, and SCP-style Git remote spellings. Callers still
// run each owning schema validator first; this function only defines equality
// across their accepted representations.
func normalizeRemoteIdentity(raw string) (string, error) {
	if raw == "" || len(raw) > 2048 || strings.Contains(raw, `\`) {
		return "", fmt.Errorf("invalid remote")
	}
	for _, char := range raw {
		if char == 0 || char < 0x20 || char == 0x7f {
			return "", fmt.Errorf("invalid remote")
		}
	}

	var host, repository string
	if match := scpRemotePattern.FindStringSubmatch(raw); match != nil {
		host = match[1]
		repository = match[2]
	} else {
		parsed, err := url.Parse(raw)
		if err != nil || parsed == nil || parsed.Opaque != "" {
			return "", fmt.Errorf("invalid remote")
		}
		switch parsed.Scheme {
		case "https":
			if parsed.User != nil {
				return "", fmt.Errorf("invalid remote")
			}
		case "ssh":
			if parsed.User == nil || parsed.User.Username() != "git" {
				return "", fmt.Errorf("invalid remote")
			}
			if _, passwordSet := parsed.User.Password(); passwordSet {
				return "", fmt.Errorf("invalid remote")
			}
		default:
			return "", fmt.Errorf("invalid remote")
		}
		if parsed.Port() != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" || strings.Contains(parsed.EscapedPath(), "%") {
			return "", fmt.Errorf("invalid remote")
		}
		host = parsed.Hostname()
		repository = strings.TrimPrefix(parsed.Path, "/")
	}

	host = strings.ToLower(strings.TrimSuffix(host, "."))
	repository = strings.TrimSuffix(repository, ".git")
	if host == "" || repository == "" || path.Clean(repository) != repository || strings.HasPrefix(repository, "/") || strings.HasSuffix(repository, "/") {
		return "", fmt.Errorf("invalid remote")
	}
	return host + "/" + repository, nil
}

func remotesEqual(left, right string) bool {
	leftIdentity, leftErr := normalizeRemoteIdentity(left)
	rightIdentity, rightErr := normalizeRemoteIdentity(right)
	return leftErr == nil && rightErr == nil && leftIdentity == rightIdentity
}
