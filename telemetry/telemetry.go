// Package telemetry provides structured logging and searchable storage for
// Codex routing decisions, model/profile selections, and verification outcomes.
package telemetry

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// EventType constants for standard Codex fleet events.
const (
	EventTypeRouting        = "routing"
	EventTypeModelSelection = "model_selection"
	EventTypeVerification   = "verification"
)

// Event represents a single recorded telemetry entry in the store.
type Event struct {
	ID         string         `json:"id"`
	Timestamp  string         `json:"timestamp"`
	EventType  string         `json:"event_type"`
	Repo       string         `json:"repo,omitempty"`
	Provider   string         `json:"provider,omitempty"`
	Model      string         `json:"model,omitempty"`
	Profile    string         `json:"profile,omitempty"`
	Role       string         `json:"role,omitempty"`
	Decision   string         `json:"decision,omitempty"`
	CheckType  string         `json:"check_type,omitempty"`
	Status     string         `json:"status,omitempty"` // pass, fail, warn
	DurationMS float64        `json:"duration_ms,omitempty"`
	Findings   int            `json:"findings,omitempty"`
	Error      string         `json:"error,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// Filter defines criteria for querying stored telemetry events.
type Filter struct {
	EventType string    `json:"event_type,omitempty"`
	Repo      string    `json:"repo,omitempty"`
	Provider  string    `json:"provider,omitempty"`
	Model     string    `json:"model,omitempty"`
	Profile   string    `json:"profile,omitempty"`
	Status    string    `json:"status,omitempty"`
	Since     time.Time `json:"since,omitempty"`
	Limit     int       `json:"limit,omitempty"`
}

var storeMu sync.Mutex

// DefaultStorePath returns the canonical path to the telemetry JSONL store.
func DefaultStorePath() string {
	if env := os.Getenv("CODEXKIT_TELEMETRY_PATH"); env != "" {
		return env
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".codex", "telemetry.jsonl")
	}
	return filepath.Join(".codex", "telemetry.jsonl")
}

// Record appends a telemetry event to the store file.
func Record(storePath string, ev Event) (Event, error) {
	if storePath == "" {
		storePath = DefaultStorePath()
	}
	if isSQLitePath(storePath) {
		s, err := getOrCreateSQLiteStore(storePath)
		if err != nil {
			return ev, err
		}
		return s.Record(context.Background(), ev)
	}
	if ev.ID == "" {
		ev.ID = generateEventID()
	}
	if ev.Timestamp == "" {
		ev.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}

	storeMu.Lock()
	defer storeMu.Unlock()

	dir := filepath.Dir(storePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return ev, fmt.Errorf("create telemetry store directory: %w", err)
	}

	data, err := json.Marshal(ev)
	if err != nil {
		return ev, fmt.Errorf("marshal telemetry event: %w", err)
	}

	f, err := os.OpenFile(storePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return ev, fmt.Errorf("open telemetry store: %w", err)
	}
	defer f.Close()

	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		return ev, fmt.Errorf("lock telemetry store: %w", err)
	}
	defer unix.Flock(int(f.Fd()), unix.LOCK_UN)

	if _, err := f.Write(append(data, '\n')); err != nil {
		return ev, fmt.Errorf("write telemetry event: %w", err)
	}

	return ev, nil
}

// Query reads events from the store matching the provided filter.
func Query(storePath string, filter Filter) ([]Event, error) {
	if storePath == "" {
		storePath = DefaultStorePath()
	}
	if isSQLitePath(storePath) {
		s, err := getOrCreateSQLiteStore(storePath)
		if err != nil {
			return nil, err
		}
		return s.Query(context.Background(), filter)
	}

	storeMu.Lock()
	defer storeMu.Unlock()

	f, err := os.Open(storePath)
	if err != nil {
		if os.IsNotExist(err) {
			return []Event{}, nil
		}
		return nil, fmt.Errorf("open telemetry store: %w", err)
	}
	defer f.Close()

	if err := unix.Flock(int(f.Fd()), unix.LOCK_SH); err != nil {
		return nil, fmt.Errorf("lock telemetry store: %w", err)
	}
	defer unix.Flock(int(f.Fd()), unix.LOCK_UN)

	var all []Event
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if matchesFilter(ev, filter) {
			all = append(all, ev)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan telemetry store: %w", err)
	}

	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}

	if filter.Limit > 0 && len(all) > filter.Limit {
		all = all[:filter.Limit]
	}

	return all, nil
}

// Search searches events matching substring keywords across all text fields.
func Search(storePath string, query string, limit int) ([]Event, error) {
	if storePath == "" {
		storePath = DefaultStorePath()
	}
	if isSQLitePath(storePath) {
		s, err := getOrCreateSQLiteStore(storePath)
		if err != nil {
			return nil, err
		}
		return s.Search(context.Background(), query, limit)
	}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return Query(storePath, Filter{Limit: limit})
	}

	storeMu.Lock()
	defer storeMu.Unlock()

	f, err := os.Open(storePath)
	if err != nil {
		if os.IsNotExist(err) {
			return []Event{}, nil
		}
		return nil, fmt.Errorf("open telemetry store: %w", err)
	}
	defer f.Close()

	if err := unix.Flock(int(f.Fd()), unix.LOCK_SH); err != nil {
		return nil, fmt.Errorf("lock telemetry store: %w", err)
	}
	defer unix.Flock(int(f.Fd()), unix.LOCK_UN)

	var all []Event
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if !strings.Contains(strings.ToLower(line), q) {
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		all = append(all, ev)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan telemetry store: %w", err)
	}

	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}

	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}

	return all, nil
}

func matchesFilter(ev Event, f Filter) bool {
	if f.EventType != "" && !strings.EqualFold(ev.EventType, f.EventType) {
		return false
	}
	if f.Repo != "" && !strings.EqualFold(ev.Repo, f.Repo) && !strings.HasSuffix(ev.Repo, "/"+f.Repo) {
		return false
	}
	if f.Provider != "" && !strings.EqualFold(ev.Provider, f.Provider) {
		return false
	}
	if f.Model != "" && !strings.EqualFold(ev.Model, f.Model) {
		return false
	}
	if f.Profile != "" && !strings.EqualFold(ev.Profile, f.Profile) {
		return false
	}
	if f.Status != "" && !strings.EqualFold(ev.Status, f.Status) {
		return false
	}
	if !f.Since.IsZero() {
		t, err := time.Parse(time.RFC3339Nano, ev.Timestamp)
		if err != nil {
			t, err = time.Parse(time.RFC3339, ev.Timestamp)
		}
		if err == nil && t.Before(f.Since) {
			return false
		}
	}
	return true
}

func generateEventID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("tel-%d-%s", time.Now().UnixNano(), hex.EncodeToString(b))
}
