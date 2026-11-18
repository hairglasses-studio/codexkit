package claimnorm

import (
	"path"
	"regexp"
	"strings"
)

const (
	ProviderAGY     = "agy"
	ProviderClaude  = "claude"
	ProviderCodex   = "codex"
	ProviderCline   = "cline"
	ProviderGeneric = "generic"
)

type Options struct {
	RepoHint      string
	WorkspaceRoot string
	KnownRepos    []string
}

type WorktreeStripResult struct {
	Path        string
	Stripped    bool
	Provider    string
	Lane        string
	NestedDepth int
}

type NormalizedPath struct {
	Raw              string
	Canonical        string
	Repo             string
	Provider         string
	WorktreeLane     string
	WorktreeStripped bool
	Physical         bool
	Host             string
}

type Claim struct {
	ID      string
	Session string
	Agent   string
	Repo    string
	Paths   []string
	Thread  string
}

type Conflict struct {
	IncomingPath       string
	ExistingClaim      Claim
	ExistingPath       string
	NormalizedIncoming NormalizedPath
	NormalizedExisting NormalizedPath
	Reason             string
}

var (
	reScheme = regexp.MustCompile(`^(?i)(https?|ssh|git|file)://`)
	reGitSSH = regexp.MustCompile(`^git@[^:]+:`)
	reHost   = regexp.MustCompile(`^([a-zA-Z0-9_\-]+):(/.*|~.*|[a-zA-Z0-9_\.\-].*)$`)

	reClaude1 = regexp.MustCompile(`(?:^|/)\.claude/worktrees/([^/]+)/(.+)$`)
	reClaude2 = regexp.MustCompile(`(?:^|/)lane-worktrees/wt-([^/]+)/(.+)$`)
	reClaude3 = regexp.MustCompile(`(?:^|/)lane-worktrees/([^/]+)/(.+)$`)
	reClaude4 = regexp.MustCompile(`(?:^|/)\.ralph/worktrees/([^/]+)/(.+)$`)

	reCodex1 = regexp.MustCompile(`(?:^|/)\.codex/worktrees/([^/]+)/(.+)$`)
	reCodex2 = regexp.MustCompile(`(?:^|/)\.codex-worktrees/([^/]+)/(.+)$`)

	reAGY1 = regexp.MustCompile(`(?:^|/)\.agents/worktrees/([^/]+)/(.+)$`)
	reAGY2 = regexp.MustCompile(`(?:^|/)\.agy/worktrees/([^/]+)/(.+)$`)
	reAGY3 = regexp.MustCompile(`(?:^|/)wt-swarm-([^/]+)/(.+)$`)
	reAGY4 = regexp.MustCompile(`(?:^|/)\.local/state/agy-swarm-worktrees/([^/]+)/(.+)$`)
	reAGY5 = regexp.MustCompile(`(?:^|/)\.local/state/agy-wave\d+-pods/([^/]+)/(.+)$`)
	reAGY6 = regexp.MustCompile(`(?:^|/)\.local/state/worktrees/([^/]+)/(.+)$`)
	reAGY7 = regexp.MustCompile(`(?:^|/)s2f_([^/]+)/(.+)$`)
	reAGY8 = regexp.MustCompile(`(?:^|/)\.hg-anchor-edit-([^/]+)/(.+)$`)

	reCline1 = regexp.MustCompile(`(?:^|/)\.cline/worktrees/([^/]+)/(.+)$`)
	reCline2 = regexp.MustCompile(`(?:^|/)\.cline-worktrees/([^/]+)/(.+)$`)

	reGeneric1 = regexp.MustCompile(`(?:^|/)\.worktrees/([^/]+)/(.+)$`)
	reGeneric2 = regexp.MustCompile(`(?:^|/)worktrees/([^/]+)/(.+)$`)
	reGeneric3 = regexp.MustCompile(`(?:^|/)\.git/worktrees/([^/]+)/(.+)$`)
	reGeneric4 = regexp.MustCompile(`^(?:/private)?/(?:tmp|var/tmp)/([^/]+)/(.+)$`)
	reGeneric5 = regexp.MustCompile(`^(?:private/)?(?:tmp|var/tmp)/([^/]+)/(.+)$`)
	reGeneric6 = regexp.MustCompile(`^wt-([^/]+)/(.+)$`)
	reGeneric7 = regexp.MustCompile(`^\.wt-([^/]+)/(.+)$`)
	reGeneric8 = regexp.MustCompile(`^([a-zA-Z0-9_\.\-]+)-wt-([^/]+)/(.+)$`)

	reSchemeHostId = regexp.MustCompile(`(?i)^([a-z0-9_-]+):(?:archglasses[-:]?([1-6])|ag([1-6])|archglasses)[-:](.*)$`)
	reHostId       = regexp.MustCompile(`(?i)^(?:archglasses[-:]?([1-6])|ag([1-6])|archglasses)[-:](.*)$`)
)

func extractHostPrefix(raw string) (string, string) {
	s := strings.TrimSpace(raw)
	s = strings.ReplaceAll(s, "\\", "/")
	if reScheme.MatchString(s) || reGitSSH.MatchString(s) {
		return "", s
	}
	m := reHost.FindStringSubmatch(s)
	if len(m) == 3 {
		host := m[1]
		rest := m[2]
		lh := strings.ToLower(host)
		if lh != "http" && lh != "https" && lh != "ssh" && lh != "git" && lh != "file" {
			return host, rest
		}
	}
	return "", s
}

func stripSingleWorktree(p string) (string, string, string, bool) {
	p = strings.TrimSpace(p)
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimPrefix(p, "./")

	if m := reClaude1.FindStringSubmatch(p); len(m) == 3 {
		return ProviderClaude, m[1], m[2], true
	}
	if m := reClaude2.FindStringSubmatch(p); len(m) == 3 {
		return ProviderClaude, m[1], m[2], true
	}
	if m := reClaude3.FindStringSubmatch(p); len(m) == 3 {
		return ProviderClaude, m[1], m[2], true
	}
	if m := reClaude4.FindStringSubmatch(p); len(m) == 3 {
		return ProviderClaude, m[1], m[2], true
	}

	if m := reCodex1.FindStringSubmatch(p); len(m) == 3 {
		return ProviderCodex, m[1], m[2], true
	}
	if m := reCodex2.FindStringSubmatch(p); len(m) == 3 {
		return ProviderCodex, m[1], m[2], true
	}

	if m := reAGY1.FindStringSubmatch(p); len(m) == 3 {
		return ProviderAGY, m[1], m[2], true
	}
	if m := reAGY2.FindStringSubmatch(p); len(m) == 3 {
		return ProviderAGY, m[1], m[2], true
	}
	if m := reAGY3.FindStringSubmatch(p); len(m) == 3 {
		return ProviderAGY, m[1], m[2], true
	}
	if m := reAGY4.FindStringSubmatch(p); len(m) == 3 {
		return ProviderAGY, m[1], m[2], true
	}
	if m := reAGY5.FindStringSubmatch(p); len(m) == 3 {
		return ProviderAGY, m[1], m[2], true
	}
	if m := reAGY6.FindStringSubmatch(p); len(m) == 3 {
		return ProviderAGY, m[1], m[2], true
	}
	if m := reAGY7.FindStringSubmatch(p); len(m) == 3 {
		return ProviderAGY, m[1], m[2], true
	}
	if m := reAGY8.FindStringSubmatch(p); len(m) == 3 {
		return ProviderAGY, m[1], m[2], true
	}

	if m := reCline1.FindStringSubmatch(p); len(m) == 3 {
		return ProviderCline, m[1], m[2], true
	}
	if m := reCline2.FindStringSubmatch(p); len(m) == 3 {
		return ProviderCline, m[1], m[2], true
	}

	if m := reGeneric1.FindStringSubmatch(p); len(m) == 3 {
		return ProviderGeneric, m[1], m[2], true
	}
	if m := reGeneric2.FindStringSubmatch(p); len(m) == 3 {
		return ProviderGeneric, m[1], m[2], true
	}
	if m := reGeneric3.FindStringSubmatch(p); len(m) == 3 {
		return ProviderGeneric, m[1], m[2], true
	}
	if m := reGeneric4.FindStringSubmatch(p); len(m) == 3 {
		return ProviderGeneric, m[1], m[2], true
	}
	if m := reGeneric5.FindStringSubmatch(p); len(m) == 3 {
		return ProviderGeneric, m[1], m[2], true
	}
	if m := reGeneric6.FindStringSubmatch(p); len(m) == 3 {
		return ProviderGeneric, m[1], m[2], true
	}
	if m := reGeneric7.FindStringSubmatch(p); len(m) == 3 {
		return ProviderGeneric, m[1], m[2], true
	}
	if m := reGeneric8.FindStringSubmatch(p); len(m) == 4 {
		return ProviderGeneric, m[2], m[3], true
	}

	return "", "", p, false
}

func InspectWorktreePath(raw string) WorktreeStripResult {
	trimmed := strings.TrimSpace(raw)
	normalized := strings.ReplaceAll(trimmed, "\\", "/")
	_, p := extractHostPrefix(normalized)

	current := p
	var finalProv string
	var finalLane string
	depth := 0

	for {
		prov, lane, sub, ok := stripSingleWorktree(current)
		if !ok || sub == "" {
			break
		}
		depth++
		current = sub
		if prov != "" && (finalProv == "" || finalProv == ProviderGeneric || prov != ProviderGeneric) {
			finalProv = prov
		}
		if lane != "" {
			finalLane = lane
		}
	}

	if depth > 0 {
		return WorktreeStripResult{
			Path:        current,
			Stripped:    true,
			Provider:    finalProv,
			Lane:        finalLane,
			NestedDepth: depth,
		}
	}

	return WorktreeStripResult{
		Path:        normalized,
		Stripped:    false,
		Provider:    "",
		Lane:        "",
		NestedDepth: 0,
	}
}

func StripWorktreePrefix(raw string) string {
	res := InspectWorktreePath(raw)
	return res.Path
}

func NormalizeRepoKey(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.ReplaceAll(s, "\\", "/")
	if s == "" || s == "." || s == ".." || s == "/" || s == ".git" {
		return ""
	}

	if idx := strings.Index(s, "://"); idx != -1 {
		rest := s[idx+3:]
		if slash := strings.Index(rest, "/"); slash != -1 {
			s = rest[slash+1:]
		} else {
			s = ""
		}
	} else if strings.HasPrefix(s, "git@") {
		if colon := strings.Index(s, ":"); colon != -1 {
			s = s[colon+1:]
		} else {
			s = ""
		}
	}

	s = strings.TrimSuffix(s, ".git")
	s = strings.Trim(s, "/")

	worktreeMarkers := []string{
		"/.claude/worktrees/",
		"/.codex/worktrees/",
		"/.codex-worktrees/",
		"/.agents/worktrees/",
		"/.agy/worktrees/",
		"/.cline/worktrees/",
		"/.cline-worktrees/",
		"/.worktrees/",
		"/worktrees/",
		"/.git/worktrees/",
		"/lane-worktrees/",
		"/.local/state/",
		"/wt-",
		"/.wt-",
	}
	for _, marker := range worktreeMarkers {
		if idx := strings.Index(s, marker); idx != -1 {
			s = s[:idx]
		}
	}

	s = strings.TrimSuffix(s, ".git")
	s = strings.Trim(s, "/")
	s = strings.ToLower(strings.TrimSpace(s))

	if s == "" || s == "." || s == ".." || s == "/" || s == ".git" {
		return ""
	}
	return s
}

func RepoLabelsMatch(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	na := NormalizeRepoKey(a)
	nb := NormalizeRepoKey(b)
	if na == "" || nb == "" {
		return false
	}
	if na == nb {
		return true
	}
	if strings.HasSuffix(na, "/"+nb) || strings.HasSuffix(nb, "/"+na) {
		return true
	}
	return false
}

func IsPhysicalLocation(raw string, opts Options) bool {
	trimmed := strings.TrimSpace(raw)
	normalized := strings.ReplaceAll(trimmed, "\\", "/")
	if normalized == "" {
		return false
	}

	res := InspectWorktreePath(normalized)
	if res.Stripped {
		return false
	}

	host, p := extractHostPrefix(normalized)

	if opts.WorkspaceRoot != "" {
		ws := strings.TrimRight(strings.ReplaceAll(opts.WorkspaceRoot, "\\", "/"), "/")
		if p == ws || strings.HasPrefix(p, ws+"/") {
			return false
		}
	}

	if host != "" {
		return true
	}

	if strings.HasPrefix(p, "~/.local/bin/") || strings.HasPrefix(p, "~/.local/etc/") {
		return true
	}

	sysPrefixes := []string{"/opt/", "/etc/", "/usr/", "/bin/", "/sbin/"}
	for _, pref := range sysPrefixes {
		if strings.HasPrefix(p, pref) {
			return true
		}
	}

	if strings.HasPrefix(p, "/var/") {
		if !strings.HasPrefix(p, "/var/tmp/") {
			return true
		}
	}

	return false
}

func cleanRelativePath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.ReplaceAll(p, "\\", "/")
	p = path.Clean(p)
	for strings.HasPrefix(p, "../") {
		p = strings.TrimPrefix(p, "../")
	}
	if p == ".." || p == "." || p == "/" || p == ".git" {
		return ""
	}
	p = strings.TrimPrefix(p, "./")
	p = strings.Trim(p, "/")
	return p
}

func NormalizeWithOptions(raw string, opts Options) NormalizedPath {
	trimmed := strings.TrimSpace(raw)
	normalized := strings.ReplaceAll(trimmed, "\\", "/")

	res := NormalizedPath{
		Raw: raw,
	}

	isPhys := IsPhysicalLocation(raw, opts)
	res.Physical = isPhys

	host, p := extractHostPrefix(normalized)
	res.Host = host

	if isPhys {
		canonPath := path.Clean(p)
		if host != "" {
			res.Canonical = CanonicalAddress(host) + ":" + canonPath
		} else {
			res.Canonical = canonPath
		}
		if opts.RepoHint != "" {
			res.Repo = NormalizeRepoKey(opts.RepoHint)
		}
		return res
	}

	if opts.WorkspaceRoot != "" {
		ws := strings.TrimRight(strings.ReplaceAll(opts.WorkspaceRoot, "\\", "/"), "/")
		if p == ws {
			p = ""
		} else if strings.HasPrefix(p, ws+"/") {
			p = strings.TrimPrefix(p, ws+"/")
		}
	}

	pBefore := p
	stripRes := InspectWorktreePath(p)
	if stripRes.Stripped {
		res.WorktreeStripped = true
		res.Provider = stripRes.Provider
		res.WorktreeLane = stripRes.Lane
		p = stripRes.Path

		if res.Repo == "" {
			for _, cand := range opts.KnownRepos {
				if cand == "" {
					continue
				}
				normCand := NormalizeRepoKey(cand)
				if normCand == "" {
					continue
				}
				candBase := path.Base(normCand)
				prefix := normCand + "/"
				prefixBase := candBase + "/"
				if strings.HasPrefix(pBefore, prefix) || strings.HasPrefix(pBefore, prefixBase) {
					res.Repo = normCand
					break
				}
				if strings.Contains(pBefore, "/"+prefix) || strings.Contains(pBefore, "/"+prefixBase) {
					res.Repo = normCand
					break
				}
			}
		}
	}

	if opts.WorkspaceRoot != "" {
		ws := strings.TrimRight(strings.ReplaceAll(opts.WorkspaceRoot, "\\", "/"), "/")
		if strings.HasPrefix(p, ws+"/") {
			p = strings.TrimPrefix(p, ws+"/")
		}
	}

	candidates := make([]string, 0, 1+len(opts.KnownRepos))
	if opts.RepoHint != "" {
		candidates = append(candidates, opts.RepoHint)
	}
	candidates = append(candidates, opts.KnownRepos...)

	p = cleanRelativePath(p)

	matchedRepo := ""
	for _, cand := range candidates {
		if cand == "" {
			continue
		}
		normCand := NormalizeRepoKey(cand)
		if normCand == "" {
			continue
		}
		candBase := path.Base(normCand)

		if strings.Contains(normCand, "/") {
			prefix := normCand + "/"
			if strings.HasPrefix(strings.ToLower(p), strings.ToLower(prefix)) {
				tail := p[len(prefix):]
				if cleanRelativePath(tail) != "" {
					matchedRepo = normCand
					p = tail
					break
				}
			}
		}

		prefix := candBase + "/"
		if strings.HasPrefix(strings.ToLower(p), strings.ToLower(prefix)) {
			tail := p[len(prefix):]
			if cleanRelativePath(tail) != "" {
				matchedRepo = normCand
				p = tail
				break
			}
		}
	}

	if matchedRepo != "" {
		res.Repo = matchedRepo
	} else if res.Repo == "" && opts.RepoHint != "" {
		res.Repo = NormalizeRepoKey(opts.RepoHint)
	}

	res.Canonical = cleanRelativePath(p)
	return res
}

func NormalizePath(raw, repoHint string) string {
	return NormalizeWithOptions(raw, Options{RepoHint: repoHint}).Canonical
}

func PathsOverlap(a, b string) bool {
	a = strings.Trim(strings.TrimSpace(a), "/")
	b = strings.Trim(strings.TrimSpace(b), "/")
	if a == "" || b == "" {
		return false
	}
	if a == "*" || a == "." || b == "*" || b == "." {
		return true
	}
	if a == b {
		return true
	}
	if strings.HasPrefix(b, a+"/") || strings.HasPrefix(a, b+"/") {
		return true
	}
	return false
}

func CanonicalHost(h string) string {
	s := strings.ToLower(strings.TrimSpace(h))
	switch s {
	case "ag1", "archglasses", "archglasses-1", "archglasses1":
		return "archglasses"
	case "ag2", "archglasses-2", "archglasses2":
		return "archglasses-2"
	case "ag3", "archglasses-3", "archglasses3":
		return "archglasses-3"
	case "ag4", "archglasses-4", "archglasses4":
		return "archglasses-4"
	case "ag5", "archglasses-5", "archglasses5":
		return "archglasses-5"
	case "ag6", "archglasses-6", "archglasses6":
		return "archglasses-6"
	}
	return s
}

func resolveHostString(n string) string {
	if n == "" || n == "1" {
		return "archglasses"
	}
	return "archglasses-" + n
}

func CanonicalAddress(addr string) string {
	s := strings.TrimSpace(addr)
	if s == "" {
		return ""
	}
	ls := strings.ToLower(s)

	if canon := CanonicalHost(ls); canon != ls {
		return canon
	}
	if ls == "archglasses" || ls == "ag1" || ls == "archglasses-1" || ls == "archglasses1" {
		return "archglasses"
	}

	if m := reSchemeHostId.FindStringSubmatch(ls); len(m) == 5 {
		scheme := m[1]
		n := m[2]
		if n == "" {
			n = m[3]
		}
		rest := m[4]
		return scheme + ":" + resolveHostString(n) + "-" + rest
	}

	if m := reHostId.FindStringSubmatch(ls); len(m) == 4 {
		n := m[1]
		if n == "" {
			n = m[2]
		}
		rest := m[3]
		return resolveHostString(n) + "-" + rest
	}

	return ls
}

func physicalPathFromCanonical(canon string) string {
	if idx := strings.Index(canon, ":"); idx != -1 {
		canon = canon[idx+1:]
	}
	canon = strings.TrimSpace(canon)
	canon = strings.ReplaceAll(canon, "\\", "/")
	return canon
}

func physicalPathsOverlap(normA, normB NormalizedPath) bool {
	hostA := CanonicalAddress(normA.Host)
	hostB := CanonicalAddress(normB.Host)
	if hostA != hostB {
		return false
	}
	pA := physicalPathFromCanonical(normA.Canonical)
	pB := physicalPathFromCanonical(normB.Canonical)

	if pA == "" || pB == "" {
		return false
	}
	cleanA := path.Clean(pA)
	cleanB := path.Clean(pB)
	if cleanA == "/" || cleanA == "." || cleanA == "*" || cleanB == "/" || cleanB == "." || cleanB == "*" {
		return true
	}
	return PathsOverlap(cleanA, cleanB)
}

func isSelfClaim(incoming, existing Claim) bool {
	if incoming.ID != "" && existing.ID != "" && CanonicalAddress(incoming.ID) == CanonicalAddress(existing.ID) {
		return true
	}
	if incoming.Session != "" && existing.Session != "" && CanonicalAddress(incoming.Session) == CanonicalAddress(existing.Session) {
		return true
	}
	if incoming.Agent != "" && existing.Agent != "" && CanonicalAddress(incoming.Agent) == CanonicalAddress(existing.Agent) {
		return true
	}
	return false
}

func CheckConflicts(incoming Claim, existing []Claim, opts Options) []Conflict {
	var conflicts []Conflict

	for _, exClaim := range existing {
		if isSelfClaim(incoming, exClaim) {
			continue
		}

		incOpts := opts
		if incoming.Repo != "" {
			incOpts.RepoHint = incoming.Repo
		}

		for _, incPath := range incoming.Paths {
			normInc := NormalizeWithOptions(incPath, incOpts)
			if incoming.Repo != "" {
				normInc.Repo = NormalizeRepoKey(incoming.Repo)
			}

			exOpts := opts
			if exClaim.Repo != "" {
				exOpts.RepoHint = exClaim.Repo
			}

			for _, exPath := range exClaim.Paths {
				normEx := NormalizeWithOptions(exPath, exOpts)
				if exClaim.Repo != "" {
					normEx.Repo = NormalizeRepoKey(exClaim.Repo)
				}

				if normInc.Physical || normEx.Physical {
					if normInc.Physical && normEx.Physical {
						if physicalPathsOverlap(normInc, normEx) {
							conflicts = append(conflicts, Conflict{
								IncomingPath:       incPath,
								ExistingClaim:      exClaim,
								ExistingPath:       exPath,
								NormalizedIncoming: normInc,
								NormalizedExisting: normEx,
								Reason:             "physical location conflict",
							})
						}
					}
					continue
				}

				if normInc.Repo != "" && normEx.Repo != "" {
					if !RepoLabelsMatch(normInc.Repo, normEx.Repo) {
						continue
					}
				}

				if PathsOverlap(normInc.Canonical, normEx.Canonical) {
					conflicts = append(conflicts, Conflict{
						IncomingPath:       incPath,
						ExistingClaim:      exClaim,
						ExistingPath:       exPath,
						NormalizedIncoming: normInc,
						NormalizedExisting: normEx,
						Reason:             "overlapping path",
					})
				}
			}
		}
	}

	return conflicts
}
