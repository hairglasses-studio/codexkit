package launchercontract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

var requiredEvidenceFields = []string{
	"schema_version",
	"source",
	"mode",
	"status",
	"observed_at",
	"candidate_path_id",
	"git_top_level_path_id",
	"git_common_dir_path_id",
	"codexkit_root_path_id",
	"repository",
	"normalized_remote",
	"tested_sha",
	"authority_sha256",
	"checkout_clean",
	"markers",
	"side_effect_guard_before_sha256",
	"side_effect_guard_after_sha256",
	"provider_started",
	"normal_launch_executed",
	"activation_eligible",
}

// LoadEvidence strictly decodes and validates one Evidence v1 JSON document.
// Unknown fields, missing required fields, duplicate keys, trailing JSON, and
// oversize documents are rejected.
func LoadEvidence(data []byte, options EvidenceOptions) (Evidence, error) {
	if len(data) == 0 || len(data) > MaxEvidenceDocumentBytes {
		return Evidence{}, fmt.Errorf("%w: document size must be between 1 and %d bytes", ErrInvalidEvidence, MaxEvidenceDocumentBytes)
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return Evidence{}, fmt.Errorf("%w: %v", ErrInvalidEvidence, err)
	}
	if err := requireEvidenceFields(data); err != nil {
		return Evidence{}, fmt.Errorf("%w: %v", ErrInvalidEvidence, err)
	}

	var evidence Evidence
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&evidence); err != nil {
		return Evidence{}, fmt.Errorf("%w: decode: %v", ErrInvalidEvidence, err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return Evidence{}, fmt.Errorf("%w: %v", ErrInvalidEvidence, err)
	}
	if err := ValidateEvidence(evidence, options); err != nil {
		return Evidence{}, err
	}
	return evidence, nil
}

func requireEvidenceFields(data []byte) error {
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&fields); err != nil {
		return fmt.Errorf("decode required fields: %v", err)
	}
	for _, field := range requiredEvidenceFields {
		if _, exists := fields[field]; !exists {
			return fmt.Errorf("missing required field %q", field)
		}
	}
	return nil
}

// MarshalEvidence validates and emits deterministic, indented Evidence v1 JSON
// with a trailing newline. It never mutates the caller's value.
func MarshalEvidence(evidence Evidence, options EvidenceOptions) ([]byte, error) {
	if err := ValidateEvidence(evidence, options); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(canonicalEvidence(evidence), "", "  ")
	if err != nil {
		return nil, fmt.Errorf("%w: encode: %v", ErrInvalidEvidence, err)
	}
	return append(data, '\n'), nil
}

func rejectDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	return requireJSONEOF(decoder)
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode: %v", err)
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("decode object key: %v", err)
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate object key %q", key)
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return fmt.Errorf("invalid object closing delimiter")
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return fmt.Errorf("invalid array closing delimiter")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	return nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return fmt.Errorf("trailing JSON: %v", err)
	}
	return nil
}
