package harnesslifecycle

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// LoadJournal strictly decodes and validates exactly one journal document.
// Unknown fields, duplicate keys, trailing values, and oversize documents fail
// closed.
func LoadJournal(data []byte) (Journal, error) {
	var journal Journal
	if err := decodeStrict(data, &journal, ErrInvalidJournal); err != nil {
		return Journal{}, err
	}
	if err := ValidateJournal(journal); err != nil {
		return Journal{}, err
	}
	return journal, nil
}

// LoadStep strictly decodes and validates exactly one step document.
func LoadStep(data []byte) (Step, error) {
	var step Step
	if err := decodeStrict(data, &step, ErrInvalidJournal); err != nil {
		return Step{}, err
	}
	if err := ValidateStep(step); err != nil {
		return Step{}, err
	}
	return step, nil
}

// LoadReceipt strictly decodes and validates exactly one receipt document.
func LoadReceipt(data []byte) (Receipt, error) {
	var receipt Receipt
	if err := decodeStrict(data, &receipt, ErrInvalidReceipt); err != nil {
		return Receipt{}, err
	}
	if err := ValidateReceipt(receipt); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

// LoadLedger strictly decodes and validates exactly one ledger document.
func LoadLedger(data []byte) (Ledger, error) {
	var ledger Ledger
	if err := decodeStrict(data, &ledger, ErrInvalidLedger); err != nil {
		return Ledger{}, err
	}
	if err := ValidateLedger(ledger); err != nil {
		return Ledger{}, err
	}
	return ledger, nil
}

// MarshalJournal validates and returns deterministic indented JSON with a
// trailing newline.
func MarshalJournal(journal Journal) ([]byte, error) {
	if err := ValidateJournal(journal); err != nil {
		return nil, err
	}
	return encodeCanonical(canonicalJournal(journal), ErrInvalidJournal)
}

// MarshalStep validates and returns deterministic indented JSON.
func MarshalStep(step Step) ([]byte, error) {
	if err := ValidateStep(step); err != nil {
		return nil, err
	}
	step.CompletedAt = step.CompletedAt.UTC().Round(0)
	return encodeCanonical(step, ErrInvalidJournal)
}

// MarshalReceipt validates and returns deterministic indented JSON.
func MarshalReceipt(receipt Receipt) ([]byte, error) {
	if err := ValidateReceipt(receipt); err != nil {
		return nil, err
	}
	return encodeCanonical(canonicalReceipt(receipt), ErrInvalidReceipt)
}

// MarshalLedger validates and returns deterministic indented JSON.
func MarshalLedger(ledger Ledger) ([]byte, error) {
	if err := ValidateLedger(ledger); err != nil {
		return nil, err
	}
	return encodeCanonical(canonicalLedger(ledger), ErrInvalidLedger)
}

func canonicalJournal(journal Journal) Journal {
	journal.OpenedAt = journal.OpenedAt.UTC().Round(0)
	journal.Intents = append([]Intent{}, journal.Intents...)
	return journal
}

func canonicalReceipt(receipt Receipt) Receipt {
	receipt.OpenedAt = receipt.OpenedAt.UTC().Round(0)
	receipt.ClosedAt = receipt.ClosedAt.UTC().Round(0)
	receipt.Results = append([]ReceiptResult{}, receipt.Results...)
	return receipt
}

func canonicalLedger(ledger Ledger) Ledger {
	ledger.UpdatedAt = ledger.UpdatedAt.UTC().Round(0)
	ledger = ledger.clone()
	if ledger.Entries == nil {
		ledger.Entries = []LedgerEntry{}
	}
	for index := range ledger.Entries {
		if ledger.Entries[index].Installed != nil {
			ledger.Entries[index].Installed.InstalledAt = ledger.Entries[index].Installed.InstalledAt.UTC().Round(0)
		}
		for tombstoneIndex := range ledger.Entries[index].Tombstones {
			ledger.Entries[index].Tombstones[tombstoneIndex].RecordedAt =
				ledger.Entries[index].Tombstones[tombstoneIndex].RecordedAt.UTC().Round(0)
		}
	}
	return ledger
}

func encodeCanonical(value any, kind error) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("%w: encode: %v", kind, err)
	}
	data = append(data, '\n')
	if len(data) > MaxDocumentBytes {
		return nil, fmt.Errorf("%w: encoded document exceeds %d bytes", kind, MaxDocumentBytes)
	}
	return data, nil
}

func decodeStrict(data []byte, target any, kind error) error {
	if len(data) == 0 || len(data) > MaxDocumentBytes {
		return fmt.Errorf("%w: document size must be between 1 and %d bytes", kind, MaxDocumentBytes)
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return fmt.Errorf("%w: %v", kind, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: decode: %v", kind, err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return fmt.Errorf("%w: %v", kind, err)
	}
	return nil
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
