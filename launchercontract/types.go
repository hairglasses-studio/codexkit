package launchercontract

import (
	"errors"
	"time"
)

const (
	// CanonicalRepository is the only repository identity accepted as the
	// private Codex harness authority.
	CanonicalRepository = "hairglasses-studio/codex-harness-staging"

	// CanonicalRemote is the normalized remote identity accepted for the
	// private Codex harness authority.
	CanonicalRemote = "github.com/" + CanonicalRepository

	// EvidenceSchemaVersion is the only launcher evidence schema understood by
	// this package.
	EvidenceSchemaVersion = 1

	// MaxEvidenceDocumentBytes bounds evidence before JSON decoding.
	MaxEvidenceDocumentBytes = 1 << 20

	// DefaultEvidenceMaxAge is used when EvidenceOptions.MaxAge is zero.
	DefaultEvidenceMaxAge = 24 * time.Hour

	maxMarkers = 64
)

var (
	// ErrInvalidContract classifies invalid launcher inputs, observations,
	// trust, and resolutions.
	ErrInvalidContract = errors.New("invalid launcher contract")

	// ErrInvalidEvidence classifies invalid launcher evidence documents.
	ErrInvalidEvidence = errors.New("invalid launcher evidence")
)

// InputValue preserves the difference between an absent input and an invalid
// explicitly supplied empty value.
type InputValue struct {
	Present bool
	Value   string
}

// Inputs are the launcher-provided discovery values. Precedence is
// CodexHarnessDir, then LegacyCodexkitRoot, then LauncherDefault.
type Inputs struct {
	CodexHarnessDir    InputValue
	LegacyCodexkitRoot InputValue
	LauncherDefault    InputValue
}

// MarkerObservation is one no-follow, regular-file marker observation below a
// candidate harness root. RelativePath is portable and root-relative.
type MarkerObservation struct {
	ID           string
	RelativePath string
	SHA256       string
	Contained    bool
	Regular      bool
}

// Observations contains externally collected facts for the selected candidate.
// Every path must already be absolute and lexical-clean.
type Observations struct {
	Candidate     string
	GitTopLevel   string
	GitCommonDir  string
	Repository    string
	RemoteURL     string
	TestedSHA     string
	CheckoutClean bool
	Markers       []MarkerObservation
}

// MarkerTrust is one required marker and its trusted content digest.
type MarkerTrust struct {
	ID           string
	RelativePath string
	SHA256       string
}

// Trust is the fixed authority, exact expected commit, and marker set against
// which observations are evaluated. Repository and NormalizedRemote must name
// this central harness.
type Trust struct {
	Repository       string
	NormalizedRemote string
	ExpectedSHA      string
	RequiredMarkers  []MarkerTrust
}

// Source identifies which launcher input selected the harness root.
type Source string

const (
	SourceCodexHarnessDir    Source = "codex-harness-dir"
	SourceLegacyCodexkitRoot Source = "legacy-codexkit-root"
	SourceLauncherDefault    Source = "launcher-default"
)

// Mode identifies the observed Git checkout layout.
type Mode string

const (
	ModePrimaryCheckout Mode = "primary-checkout"
	ModeLinkedWorktree  Mode = "linked-worktree"
)

// Resolution is returned only after the selected candidate satisfies the full
// central-harness trust contract. Its paths remain runtime values and must not
// be serialized directly; NewEvidence converts them to sanitized path IDs.
type Resolution struct {
	Source             Source              `json:"-"`
	Mode               Mode                `json:"-"`
	Candidate          string              `json:"-"`
	CodexkitRoot       string              `json:"-"`
	GitTopLevel        string              `json:"-"`
	GitCommonDir       string              `json:"-"`
	Repository         string              `json:"-"`
	NormalizedRemote   string              `json:"-"`
	TestedSHA          string              `json:"-"`
	CheckoutClean      bool                `json:"-"`
	Markers            []MarkerObservation `json:"-"`
	AuthoritySHA256    string              `json:"-"`
	ActivationEligible bool                `json:"-"`
}

// Status is the closed launcher-evidence result set. Evidence v1 records only
// successful preflight authorization; rejected inputs return an error before
// evidence can claim activation eligibility.
type Status string

const StatusActivationEligible Status = "activation-eligible"

// EvidenceMarker is a sanitized marker identity. PathID is a one-way digest,
// never a host path.
type EvidenceMarker struct {
	ID                    string `json:"id"`
	PathID                string `json:"path_id"`
	AuthorityMarkerPathID string `json:"authority_marker_path_id"`
	SHA256                string `json:"sha256"`
}

// Evidence is the strict, public launcher preflight record. It contains no
// host path; every path-bearing runtime value is represented by a PathID.
type Evidence struct {
	SchemaVersion               int              `json:"schema_version"`
	Source                      Source           `json:"source"`
	Mode                        Mode             `json:"mode"`
	Status                      Status           `json:"status"`
	ObservedAt                  time.Time        `json:"observed_at"`
	CandidatePathID             string           `json:"candidate_path_id"`
	GitTopLevelPathID           string           `json:"git_top_level_path_id"`
	GitCommonDirPathID          string           `json:"git_common_dir_path_id"`
	CodexkitRootPathID          string           `json:"codexkit_root_path_id"`
	Repository                  string           `json:"repository"`
	NormalizedRemote            string           `json:"normalized_remote"`
	TestedSHA                   string           `json:"tested_sha"`
	AuthoritySHA256             string           `json:"authority_sha256"`
	CheckoutClean               bool             `json:"checkout_clean"`
	Markers                     []EvidenceMarker `json:"markers"`
	SideEffectGuardBeforeSHA256 string           `json:"side_effect_guard_before_sha256"`
	SideEffectGuardAfterSHA256  string           `json:"side_effect_guard_after_sha256"`
	ProviderStarted             bool             `json:"provider_started"`
	NormalLaunchExecuted        bool             `json:"normal_launch_executed"`
	ActivationEligible          bool             `json:"activation_eligible"`
}

// EvidenceRecord supplies externally observed, non-path metadata used to build
// Evidence. Equal guard hashes prove that the collector's declared guarded
// state did not change during this preflight-only check.
type EvidenceRecord struct {
	ObservedAt                  time.Time
	SideEffectGuardBeforeSHA256 string
	SideEffectGuardAfterSHA256  string
	ProviderStarted             bool
	NormalLaunchExecuted        bool
}

// EvidenceOptions supplies the trusted validation clock and authority digest.
// Now and ExpectedAuthoritySHA256 are required so validation consults neither
// ambient time nor authority asserted by the evidence itself. A zero MaxAge
// selects DefaultEvidenceMaxAge.
type EvidenceOptions struct {
	Now                     time.Time
	MaxAge                  time.Duration
	ExpectedAuthoritySHA256 string
}
