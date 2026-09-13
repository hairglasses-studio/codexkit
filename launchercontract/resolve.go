package launchercontract

import (
	"fmt"
	"path"
)

func validateInputShape(name string, input InputValue) error {
	if !input.Present && input.Value != "" {
		return fmt.Errorf("%w: absent %s must not carry a value", ErrInvalidContract, name)
	}
	return nil
}

func selectCandidate(inputs Inputs) (string, Source, error) {
	if err := validateInputShape("CODEX_HARNESS_DIR", inputs.CodexHarnessDir); err != nil {
		return "", "", err
	}
	if err := validateInputShape("CODEXKIT_ROOT", inputs.LegacyCodexkitRoot); err != nil {
		return "", "", err
	}
	if err := validateInputShape("launcher default", inputs.LauncherDefault); err != nil {
		return "", "", err
	}

	if inputs.CodexHarnessDir.Present {
		candidate := inputs.CodexHarnessDir.Value
		if err := cleanAbsolutePath(candidate); err != nil {
			return "", "", fmt.Errorf("%w: CODEX_HARNESS_DIR %v", ErrInvalidContract, err)
		}
		if inputs.LegacyCodexkitRoot.Present {
			legacy := inputs.LegacyCodexkitRoot.Value
			if err := cleanAbsolutePath(legacy); err != nil {
				return "", "", fmt.Errorf("%w: CODEXKIT_ROOT %v", ErrInvalidContract, err)
			}
			if legacy != path.Join(candidate, "codexkit") {
				return "", "", fmt.Errorf("%w: CODEXKIT_ROOT must equal CODEX_HARNESS_DIR/codexkit", ErrInvalidContract)
			}
		}
		return candidate, SourceCodexHarnessDir, nil
	}

	if inputs.LegacyCodexkitRoot.Present {
		legacy := inputs.LegacyCodexkitRoot.Value
		if err := cleanAbsolutePath(legacy); err != nil {
			return "", "", fmt.Errorf("%w: CODEXKIT_ROOT %v", ErrInvalidContract, err)
		}
		if path.Base(legacy) != "codexkit" {
			return "", "", fmt.Errorf("%w: CODEXKIT_ROOT must identify harness/codexkit", ErrInvalidContract)
		}
		candidate := path.Dir(legacy)
		if err := cleanAbsolutePath(candidate); err != nil {
			return "", "", fmt.Errorf("%w: derived harness root %v", ErrInvalidContract, err)
		}
		return candidate, SourceLegacyCodexkitRoot, nil
	}

	if inputs.LauncherDefault.Present {
		candidate := inputs.LauncherDefault.Value
		if err := cleanAbsolutePath(candidate); err != nil {
			return "", "", fmt.Errorf("%w: launcher default %v", ErrInvalidContract, err)
		}
		return candidate, SourceLauncherDefault, nil
	}

	return "", "", fmt.Errorf("%w: no launcher candidate is configured", ErrInvalidContract)
}

// Resolve applies launcher precedence and validates externally supplied facts.
// It never falls back after a present higher-precedence value fails validation.
func Resolve(inputs Inputs, observations Observations, trust Trust) (Resolution, error) {
	candidate, source, err := selectCandidate(inputs)
	if err != nil {
		return Resolution{}, err
	}
	if err := validateTrust(trust); err != nil {
		return Resolution{}, err
	}
	if observations.Candidate != candidate {
		return Resolution{}, fmt.Errorf("%w: observations do not bind the selected candidate", ErrInvalidContract)
	}
	if err := cleanAbsolutePath(observations.Candidate); err != nil {
		return Resolution{}, fmt.Errorf("%w: observed candidate %v", ErrInvalidContract, err)
	}
	if observations.GitTopLevel != candidate {
		return Resolution{}, fmt.Errorf("%w: git top-level must exactly equal the selected candidate", ErrInvalidContract)
	}
	if err := cleanAbsolutePath(observations.GitTopLevel); err != nil {
		return Resolution{}, fmt.Errorf("%w: git top-level %v", ErrInvalidContract, err)
	}
	if err := cleanAbsolutePath(observations.GitCommonDir); err != nil {
		return Resolution{}, fmt.Errorf("%w: git common-dir %v", ErrInvalidContract, err)
	}
	if observations.GitCommonDir == candidate {
		return Resolution{}, fmt.Errorf("%w: git common-dir must not equal the worktree", ErrInvalidContract)
	}
	if observations.Repository != trust.Repository || observations.Repository != CanonicalRepository {
		return Resolution{}, fmt.Errorf("%w: observed repository is not the central harness", ErrInvalidContract)
	}
	normalizedRemote, err := normalizeRemote(observations.RemoteURL)
	if err != nil {
		return Resolution{}, fmt.Errorf("%w: %v", ErrInvalidContract, err)
	}
	if normalizedRemote != trust.NormalizedRemote {
		return Resolution{}, fmt.Errorf("%w: observed remote does not match trust", ErrInvalidContract)
	}
	if !validTestedSHA(observations.TestedSHA) {
		return Resolution{}, fmt.Errorf("%w: tested SHA must be a nonzero lowercase 40-hex commit", ErrInvalidContract)
	}
	if observations.TestedSHA != trust.ExpectedSHA {
		return Resolution{}, fmt.Errorf("%w: tested SHA does not match the trusted expected SHA", ErrInvalidContract)
	}
	if !observations.CheckoutClean {
		return Resolution{}, fmt.Errorf("%w: checkout is dirty", ErrInvalidContract)
	}
	if err := validateMarkers(observations.Markers, trust.RequiredMarkers); err != nil {
		return Resolution{}, fmt.Errorf("%w: %v", ErrInvalidContract, err)
	}
	authoritySHA256, err := AuthoritySHA256(trust)
	if err != nil {
		return Resolution{}, err
	}

	resolution := Resolution{
		Source:             source,
		Mode:               checkoutMode(candidate, observations.GitCommonDir),
		Candidate:          candidate,
		CodexkitRoot:       path.Join(candidate, "codexkit"),
		GitTopLevel:        observations.GitTopLevel,
		GitCommonDir:       observations.GitCommonDir,
		Repository:         observations.Repository,
		NormalizedRemote:   normalizedRemote,
		TestedSHA:          observations.TestedSHA,
		CheckoutClean:      true,
		Markers:            canonicalMarkers(observations.Markers),
		AuthoritySHA256:    authoritySHA256,
		ActivationEligible: true,
	}
	if err := ValidateResolution(resolution); err != nil {
		return Resolution{}, err
	}
	return resolution, nil
}

// MarshalJSON refuses generic serialization because Resolution contains live
// host paths. Callers must use NewEvidence and MarshalEvidence instead.
func (Resolution) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: Resolution must be converted to sanitized Evidence before JSON serialization", ErrInvalidContract)
}
