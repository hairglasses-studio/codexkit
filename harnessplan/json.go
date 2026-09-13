package harnessplan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
)

// Load strictly decodes and validates exactly one plan envelope. Unknown
// fields, duplicate keys, trailing values, oversize documents, and digest
// mismatches fail closed.
func Load(data []byte, options ValidationOptions) (PlanEnvelope, error) {
	if len(data) == 0 || len(data) > MaxDocumentBytes {
		return PlanEnvelope{}, fmt.Errorf("%w: document size must be between 1 and %d bytes", ErrInvalidPlan, MaxDocumentBytes)
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return PlanEnvelope{}, fmt.Errorf("%w: %v", ErrInvalidPlan, err)
	}

	var envelope PlanEnvelope
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return PlanEnvelope{}, fmt.Errorf("%w: decode: %v", ErrInvalidPlan, err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return PlanEnvelope{}, fmt.Errorf("%w: %v", ErrInvalidPlan, err)
	}
	if err := Validate(envelope, options); err != nil {
		return PlanEnvelope{}, err
	}
	return envelope, nil
}

// Seal validates a plan against trusted inputs and returns its digest-bearing
// envelope.
func Seal(plan Plan, options ValidationOptions) (PlanEnvelope, error) {
	if err := validatePlan(plan, options); err != nil {
		return PlanEnvelope{}, err
	}
	digest, err := digestCanonicalPlan(plan)
	if err != nil {
		return PlanEnvelope{}, err
	}
	return PlanEnvelope{SchemaVersion: SchemaVersion, PlanSHA256: digest, Plan: plan}, nil
}

// Digest returns the canonical SHA-256 digest for a valid plan.
func Digest(plan Plan, options ValidationOptions) (string, error) {
	if err := validatePlan(plan, options); err != nil {
		return "", err
	}
	return digestCanonicalPlan(plan)
}

// Marshal validates and returns deterministic indented JSON with a trailing
// newline. It does not mutate the caller's value.
func Marshal(envelope PlanEnvelope, options ValidationOptions) ([]byte, error) {
	if err := Validate(envelope, options); err != nil {
		return nil, err
	}
	canonical := canonicalEnvelope(envelope)
	data, err := json.MarshalIndent(canonical, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("%w: encode: %v", ErrInvalidPlan, err)
	}
	return append(data, '\n'), nil
}

func digestCanonicalPlan(plan Plan) (string, error) {
	data, err := json.Marshal(canonicalPlan(plan))
	if err != nil {
		return "", fmt.Errorf("%w: encode plan digest: %v", ErrInvalidPlan, err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func canonicalEnvelope(envelope PlanEnvelope) PlanEnvelope {
	envelope.Plan = canonicalPlan(envelope.Plan)
	return envelope
}

func canonicalPlan(plan Plan) Plan {
	plan.SourceBindings = slices.Clone(plan.SourceBindings)
	for index := range plan.SourceBindings {
		plan.SourceBindings[index].ObservedAt = plan.SourceBindings[index].ObservedAt.UTC().Round(0)
		plan.SourceBindings[index].ValidUntil = plan.SourceBindings[index].ValidUntil.UTC().Round(0)
	}
	sort.Slice(plan.SourceBindings, func(i, j int) bool {
		return plan.SourceBindings[i].ID < plan.SourceBindings[j].ID
	})
	// Nil and empty both mean a valid no-op plan. Normalize them to the same
	// canonical empty JSON array and therefore the same plan digest.
	plan.Operations = append([]Operation{}, plan.Operations...)
	for index := range plan.Operations {
		plan.Operations[index].SourceBindingIDs = slices.Clone(plan.Operations[index].SourceBindingIDs)
		sort.Strings(plan.Operations[index].SourceBindingIDs)
		plan.Operations[index].Before = cloneFileState(plan.Operations[index].Before)
		plan.Operations[index].After = cloneFileState(plan.Operations[index].After)
		if payload := plan.Operations[index].Payload; payload != nil {
			copy := *payload
			plan.Operations[index].Payload = &copy
		}
	}
	sort.Slice(plan.Operations, func(i, j int) bool {
		if plan.Operations[i].Destination != plan.Operations[j].Destination {
			return plan.Operations[i].Destination < plan.Operations[j].Destination
		}
		return plan.Operations[i].ID < plan.Operations[j].ID
	})
	return plan
}

func cloneFileState(state FileState) FileState {
	if state.Marker != nil {
		copy := *state.Marker
		state.Marker = &copy
	}
	return state
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
