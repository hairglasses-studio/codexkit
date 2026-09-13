package harnessmanifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

// Load strictly decodes and validates a manifest. Unknown fields and trailing
// JSON values fail closed.
func Load(data []byte, options ValidationOptions) (Manifest, error) {
	if len(data) == 0 || len(data) > MaxDocumentBytes {
		return Manifest{}, fmt.Errorf("decode projection manifest: document size must be between 1 and %d bytes", MaxDocumentBytes)
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return Manifest{}, fmt.Errorf("decode projection manifest: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode projection manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Manifest{}, fmt.Errorf("decode projection manifest: trailing JSON value")
		}
		return Manifest{}, fmt.Errorf("decode projection manifest: %w", err)
	}
	if err := Validate(manifest, options); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
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
		return fmt.Errorf("decode: %w", err)
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
				return fmt.Errorf("decode object key: %w", err)
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
		return fmt.Errorf("trailing JSON: %w", err)
	}
	return nil
}

// Marshal validates and returns canonical indented JSON with a trailing
// newline. Set-like slices are sorted on a copy; ordered semantic fields are
// not modified.
func Marshal(manifest Manifest, options ValidationOptions) ([]byte, error) {
	if err := Validate(manifest, options); err != nil {
		return nil, err
	}
	canonical := canonicalManifest(manifest)
	data, err := json.MarshalIndent(canonical, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal projection manifest: %w", err)
	}
	return append(data, '\n'), nil
}

func canonicalManifest(manifest Manifest) Manifest {
	manifest.Objects = append([]Object(nil), manifest.Objects...)
	for index := range manifest.Objects {
		manifest.Objects[index].VerifierIDs = canonicalVerifierIDs(manifest.Objects[index].VerifierIDs)
	}
	sort.Slice(manifest.Objects, func(i, j int) bool {
		if manifest.Objects[i].ID != manifest.Objects[j].ID {
			return manifest.Objects[i].ID < manifest.Objects[j].ID
		}
		return manifest.Objects[i].Destination < manifest.Objects[j].Destination
	})
	manifest.Omissions = append([]Omission(nil), manifest.Omissions...)
	for index := range manifest.Omissions {
		manifest.Omissions[index].VerifierIDs = canonicalVerifierIDs(manifest.Omissions[index].VerifierIDs)
	}
	sort.Slice(manifest.Omissions, func(i, j int) bool {
		if manifest.Omissions[i].ID != manifest.Omissions[j].ID {
			return manifest.Omissions[i].ID < manifest.Omissions[j].ID
		}
		return manifest.Omissions[i].Path < manifest.Omissions[j].Path
	})
	manifest.CoverageInputs = append([]CoverageInput(nil), manifest.CoverageInputs...)
	sort.Slice(manifest.CoverageInputs, func(i, j int) bool {
		if manifest.CoverageInputs[i].Path != manifest.CoverageInputs[j].Path {
			return manifest.CoverageInputs[i].Path < manifest.CoverageInputs[j].Path
		}
		return manifest.CoverageInputs[i].ID < manifest.CoverageInputs[j].ID
	})
	return manifest
}
