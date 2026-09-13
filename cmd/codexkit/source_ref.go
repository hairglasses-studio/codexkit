package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/hairglasses-studio/codexkit/sourceref"
)

type sourceRefSummary struct {
	SchemaVersion   int       `json:"schema_version"`
	CheckedAt       time.Time `json:"checked_at"`
	MaxAge          string    `json:"max_age"`
	InventorySHA256 string    `json:"inventory_sha256"`
	Repositories    int       `json:"repositories"`
	Authoritative   int       `json:"authoritative"`
	Primary         int       `json:"primary_checkout"`
	LiveRuntime     int       `json:"live_runtime"`
	LocalOnly       int       `json:"local_only"`
	Dirty           int       `json:"dirty"`
}

func runSourceRef(args []string) {
	summary, jsonOutput, err := executeSourceRef(args, time.Now().UTC())
	if err != nil {
		fmt.Fprintf(os.Stderr, "source-ref check: %v\n", err)
		os.Exit(1)
	}
	if jsonOutput {
		printJSON(summary)
		return
	}
	fmt.Printf("source-ref check: PASS (repositories=%d authoritative=%d primary=%d live=%d local-only=%d dirty=%d inventory-sha256=%s)\n",
		summary.Repositories, summary.Authoritative, summary.Primary, summary.LiveRuntime, summary.LocalOnly, summary.Dirty, summary.InventorySHA256)
}

func executeSourceRef(args []string, defaultNow time.Time) (sourceRefSummary, bool, error) {
	if len(args) < 2 || args[0] != "check" {
		return sourceRefSummary{}, false, fmt.Errorf("usage: codexkit source-ref check <inventory.json> [--json] [--now <RFC3339>] [--max-age <duration>]")
	}
	path := args[1]
	now := defaultNow.UTC().Round(0)
	maxAge := sourceref.DefaultMaxAge
	jsonOutput := false
	for index := 2; index < len(args); index++ {
		switch args[index] {
		case "--json":
			jsonOutput = true
		case "--now", "--max-age":
			if index+1 >= len(args) {
				return sourceRefSummary{}, false, fmt.Errorf("%s requires a value", args[index])
			}
			value := args[index+1]
			if args[index] == "--now" {
				parsed, err := time.Parse(time.RFC3339, value)
				if err != nil {
					return sourceRefSummary{}, false, fmt.Errorf("parse --now: %w", err)
				}
				now = parsed.UTC().Round(0)
			} else {
				parsed, err := time.ParseDuration(value)
				if err != nil || parsed <= 0 {
					return sourceRefSummary{}, false, fmt.Errorf("--max-age must be a positive Go duration")
				}
				maxAge = parsed
			}
			index++
		default:
			if strings.HasPrefix(args[index], "-") {
				return sourceRefSummary{}, false, fmt.Errorf("unknown source-ref flag %q", args[index])
			}
			return sourceRefSummary{}, false, fmt.Errorf("unexpected source-ref argument %q", args[index])
		}
	}

	data, err := readSourceRefInventory(path)
	if err != nil {
		return sourceRefSummary{}, false, err
	}
	opts := sourceref.Options{Now: now, MaxAge: maxAge}
	inventory, err := sourceref.Load(data, opts)
	if err != nil {
		return sourceRefSummary{}, false, err
	}
	canonical, err := sourceref.Marshal(inventory, opts)
	if err != nil {
		return sourceRefSummary{}, false, err
	}
	digest := sha256.Sum256(canonical)
	summary := sourceRefSummary{
		SchemaVersion: inventory.SchemaVersion, CheckedAt: now, MaxAge: maxAge.String(),
		InventorySHA256: hex.EncodeToString(digest[:]), Repositories: len(inventory.Repositories),
	}
	for _, evidence := range inventory.Repositories {
		if evidence.Checkout.Dirty {
			summary.Dirty++
		}
		switch evidence.EvidenceClass {
		case sourceref.EvidenceRemoteTree, sourceref.EvidenceFreshWorktree:
			summary.Authoritative++
		case sourceref.EvidencePrimaryCheckout:
			summary.Primary++
		case sourceref.EvidenceLiveRuntime:
			summary.LiveRuntime++
		case sourceref.EvidenceLocalOnly:
			summary.LocalOnly++
		}
	}
	return summary, jsonOutput, nil
}

func readSourceRefInventory(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open inventory: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, sourceref.MaxDocumentBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read inventory: %w", err)
	}
	if len(data) > sourceref.MaxDocumentBytes {
		return nil, fmt.Errorf("inventory exceeds %d bytes", sourceref.MaxDocumentBytes)
	}
	return data, nil
}
