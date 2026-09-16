package telemetry

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hairglasses-studio/codexkit/internal/sqlitex"
)

type SQLiteStore struct {
	db     *sql.DB
	dbPath string
}

var (
	sqliteStoresMu sync.Mutex
	sqliteStores   = make(map[string]*SQLiteStore)
)

func DefaultSQLitePath() string {
	if env := os.Getenv("CODEXKIT_TELEMETRY_PATH"); env != "" && isSQLitePath(env) {
		return env
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".codex", "telemetry.sqlite")
	}
	return filepath.Join(".codex", "telemetry.sqlite")
}

func isSQLitePath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".sqlite" || ext == ".db" || ext == ".sqlite3"
}

func getOrCreateSQLiteStore(dbPath string) (*SQLiteStore, error) {
	if dbPath == "" {
		dbPath = DefaultSQLitePath()
	}
	absPath, err := filepath.Abs(dbPath)
	if err == nil {
		dbPath = absPath
	}

	sqliteStoresMu.Lock()
	defer sqliteStoresMu.Unlock()

	if s, ok := sqliteStores[dbPath]; ok {
		return s, nil
	}

	s, err := NewSQLiteStore(dbPath)
	if err != nil {
		return nil, err
	}
	sqliteStores[dbPath] = s
	return s, nil
}

func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	if dbPath == "" {
		dbPath = DefaultSQLitePath()
	}

	opts := sqlitex.DefaultOptions()
	opts.BusyTimeoutMS = 5000
	db, err := sqlitex.Open("sqlite", dbPath, opts)
	if err != nil {
		return nil, err
	}

	schema := `
	CREATE TABLE IF NOT EXISTS telemetry_events (
		id TEXT PRIMARY KEY,
		timestamp TEXT NOT NULL,
		event_type TEXT NOT NULL,
		repo TEXT,
		provider TEXT,
		model TEXT,
		profile TEXT,
		role TEXT,
		decision TEXT,
		check_type TEXT,
		status TEXT,
		duration_ms REAL,
		findings INTEGER,
		error TEXT,
		metadata_json TEXT
	);
	CREATE INDEX IF NOT EXISTS idx_telem_type_time ON telemetry_events(event_type, timestamp DESC);
	CREATE INDEX IF NOT EXISTS idx_telem_repo_time ON telemetry_events(repo, timestamp DESC);
	CREATE INDEX IF NOT EXISTS idx_telem_status ON telemetry_events(status);
	`
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init telemetry schema: %w", err)
	}

	return &SQLiteStore{db: db, dbPath: dbPath}, nil
}

func (s *SQLiteStore) Close() error {
	sqliteStoresMu.Lock()
	delete(sqliteStores, s.dbPath)
	sqliteStoresMu.Unlock()
	return s.db.Close()
}

func (s *SQLiteStore) DB() *sql.DB {
	return s.db
}

func (s *SQLiteStore) Record(ctx context.Context, ev Event) (Event, error) {
	if ev.ID == "" {
		ev.ID = generateEventID()
	}
	if ev.Timestamp == "" {
		ev.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}

	metaJSON, err := json.Marshal(ev.Metadata)
	if err != nil {
		return ev, fmt.Errorf("marshal metadata: %w", err)
	}

	query := `
	INSERT INTO telemetry_events (
		id, timestamp, event_type, repo, provider, model, profile,
		role, decision, check_type, status, duration_ms, findings, error, metadata_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	err = sqlitex.WithRetry(ctx, sqlitex.DefaultRetryConfig(), func() error {
		return sqlitex.WithImmediateTx(ctx, s.db, func(tx *sql.Tx) error {
			_, execErr := tx.ExecContext(ctx, query,
				ev.ID,
				ev.Timestamp,
				ev.EventType,
				ev.Repo,
				ev.Provider,
				ev.Model,
				ev.Profile,
				ev.Role,
				ev.Decision,
				ev.CheckType,
				ev.Status,
				ev.DurationMS,
				ev.Findings,
				ev.Error,
				string(metaJSON),
			)
			return execErr
		})
	})

	if err != nil {
		return ev, fmt.Errorf("record telemetry event: %w", err)
	}
	return ev, nil
}

func (s *SQLiteStore) Query(ctx context.Context, filter Filter) ([]Event, error) {
	var conds []string
	var args []any

	if filter.EventType != "" {
		conds = append(conds, "LOWER(event_type) = LOWER(?)")
		args = append(args, filter.EventType)
	}
	if filter.Repo != "" {
		conds = append(conds, "(LOWER(repo) = LOWER(?) OR LOWER(repo) LIKE LOWER(?))")
		args = append(args, filter.Repo, "%/"+filter.Repo)
	}
	if filter.Provider != "" {
		conds = append(conds, "LOWER(provider) = LOWER(?)")
		args = append(args, filter.Provider)
	}
	if filter.Model != "" {
		conds = append(conds, "LOWER(model) = LOWER(?)")
		args = append(args, filter.Model)
	}
	if filter.Profile != "" {
		conds = append(conds, "LOWER(profile) = LOWER(?)")
		args = append(args, filter.Profile)
	}
	if filter.Status != "" {
		conds = append(conds, "LOWER(status) = LOWER(?)")
		args = append(args, filter.Status)
	}
	if !filter.Since.IsZero() {
		conds = append(conds, "timestamp >= ?")
		args = append(args, filter.Since.Format(time.RFC3339Nano))
	}

	whereClause := ""
	if len(conds) > 0 {
		whereClause = "WHERE " + strings.Join(conds, " AND ")
	}

	limitClause := ""
	if filter.Limit > 0 {
		limitClause = fmt.Sprintf("LIMIT %d", filter.Limit)
	}

	query := fmt.Sprintf(`
	SELECT id, timestamp, event_type, repo, provider, model, profile,
	       role, decision, check_type, status, duration_ms, findings, error, metadata_json
	FROM telemetry_events
	%s
	ORDER BY timestamp DESC
	%s
	`, whereClause, limitClause)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query telemetry: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var ev Event
		var metaStr string
		var dur sql.NullFloat64
		var find sql.NullInt64

		err := rows.Scan(
			&ev.ID,
			&ev.Timestamp,
			&ev.EventType,
			&ev.Repo,
			&ev.Provider,
			&ev.Model,
			&ev.Profile,
			&ev.Role,
			&ev.Decision,
			&ev.CheckType,
			&ev.Status,
			&dur,
			&find,
			&ev.Error,
			&metaStr,
		)
		if err != nil {
			return nil, fmt.Errorf("scan telemetry row: %w", err)
		}

		if dur.Valid {
			ev.DurationMS = dur.Float64
		}
		if find.Valid {
			ev.Findings = int(find.Int64)
		}
		if metaStr != "" && metaStr != "null" {
			_ = json.Unmarshal([]byte(metaStr), &ev.Metadata)
		}
		events = append(events, ev)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows telemetry: %w", err)
	}

	if events == nil {
		events = []Event{}
	}
	return events, nil
}

func (s *SQLiteStore) Search(ctx context.Context, query string, limit int) ([]Event, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return s.Query(ctx, Filter{Limit: limit})
	}

	pattern := "%" + strings.ToLower(q) + "%"
	limitClause := ""
	if limit > 0 {
		limitClause = fmt.Sprintf("LIMIT %d", limit)
	}

	sqlQuery := fmt.Sprintf(`
	SELECT id, timestamp, event_type, repo, provider, model, profile,
	       role, decision, check_type, status, duration_ms, findings, error, metadata_json
	FROM telemetry_events
	WHERE LOWER(id) LIKE ?
	   OR LOWER(event_type) LIKE ?
	   OR LOWER(repo) LIKE ?
	   OR LOWER(provider) LIKE ?
	   OR LOWER(model) LIKE ?
	   OR LOWER(profile) LIKE ?
	   OR LOWER(role) LIKE ?
	   OR LOWER(decision) LIKE ?
	   OR LOWER(check_type) LIKE ?
	   OR LOWER(status) LIKE ?
	   OR LOWER(error) LIKE ?
	   OR LOWER(metadata_json) LIKE ?
	ORDER BY timestamp DESC
	%s
	`, limitClause)

	args := []any{
		pattern, pattern, pattern, pattern, pattern, pattern,
		pattern, pattern, pattern, pattern, pattern, pattern,
	}

	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("search telemetry: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var ev Event
		var metaStr string
		var dur sql.NullFloat64
		var find sql.NullInt64

		err := rows.Scan(
			&ev.ID,
			&ev.Timestamp,
			&ev.EventType,
			&ev.Repo,
			&ev.Provider,
			&ev.Model,
			&ev.Profile,
			&ev.Role,
			&ev.Decision,
			&ev.CheckType,
			&ev.Status,
			&dur,
			&find,
			&ev.Error,
			&metaStr,
		)
		if err != nil {
			return nil, fmt.Errorf("scan search row: %w", err)
		}

		if dur.Valid {
			ev.DurationMS = dur.Float64
		}
		if find.Valid {
			ev.Findings = int(find.Int64)
		}
		if metaStr != "" && metaStr != "null" {
			_ = json.Unmarshal([]byte(metaStr), &ev.Metadata)
		}
		events = append(events, ev)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows search: %w", err)
	}

	if events == nil {
		events = []Event{}
	}
	return events, nil
}
