package sqlitex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Options struct {
	ReadOnly        bool
	BusyTimeoutMS   int
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

func DefaultOptions() Options {
	return Options{
		ReadOnly:        false,
		BusyTimeoutMS:   5000,
		MaxOpenConns:    1,
		MaxIdleConns:    1,
		ConnMaxLifetime: 1 * time.Hour,
	}
}

func ReadOnlyOptions() Options {
	return Options{
		ReadOnly:        true,
		BusyTimeoutMS:   10000,
		MaxOpenConns:    8,
		MaxIdleConns:    4,
		ConnMaxLifetime: 1 * time.Hour,
	}
}

func Open(driverName, dbPath string, opts Options) (*sql.DB, error) {
	if strings.TrimSpace(dbPath) == "" {
		return nil, errors.New("sqlitex: dbPath cannot be empty")
	}
	if driverName == "" {
		driverName = "sqlite"
	}
	if opts.BusyTimeoutMS <= 0 {
		opts.BusyTimeoutMS = 5000
	}

	if !opts.ReadOnly && dbPath != ":memory:" && !strings.HasPrefix(dbPath, "file::memory:") {
		cleanPath := strings.TrimPrefix(dbPath, "file:")
		if idx := strings.Index(cleanPath, "?"); idx != -1 {
			cleanPath = cleanPath[:idx]
		}
		dir := filepath.Dir(cleanPath)
		if dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return nil, fmt.Errorf("sqlitex: mkdir %s: %w", dir, err)
			}
		}
	}

	dsn := buildHardenedDSN(dbPath, opts)
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlitex: open %s: %w", dbPath, err)
	}

	if opts.ReadOnly {
		maxOpen := opts.MaxOpenConns
		if maxOpen <= 0 {
			maxOpen = 8
		}
		db.SetMaxOpenConns(maxOpen)
		maxIdle := opts.MaxIdleConns
		if maxIdle <= 0 {
			maxIdle = 4
		}
		db.SetMaxIdleConns(maxIdle)
	} else {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	}
	if opts.ConnMaxLifetime > 0 {
		db.SetConnMaxLifetime(opts.ConnMaxLifetime)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(opts.BusyTimeoutMS)*time.Millisecond)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlitex: ping %s: %w", dbPath, err)
	}

	if err := applyPragmas(ctx, db, opts); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlitex: apply pragmas: %w", err)
	}

	return db, nil
}

func buildHardenedDSN(dbPath string, opts Options) string {
	q := url.Values{}
	timeoutStr := fmt.Sprintf("%d", opts.BusyTimeoutMS)

	if opts.ReadOnly {
		q.Set("mode", "ro")
		q.Set("_query_only", "true")
	} else {
		q.Set("_journal_mode", "WAL")
		q.Set("_synchronous", "NORMAL")
		q.Set("_txlock", "immediate")
	}
	q.Set("_busy_timeout", timeoutStr)
	q.Set("_foreign_keys", "ON")

	if strings.HasPrefix(dbPath, "file:") {
		sep := "?"
		if strings.Contains(dbPath, "?") {
			sep = "&"
		}
		return dbPath + sep + q.Encode()
	}
	return fmt.Sprintf("file:%s?%s", dbPath, q.Encode())
}

func applyPragmas(ctx context.Context, db *sql.DB, opts Options) error {
	pragmas := []string{
		fmt.Sprintf("PRAGMA busy_timeout = %d;", opts.BusyTimeoutMS),
		"PRAGMA foreign_keys = ON;",
	}
	if opts.ReadOnly {
		pragmas = append(pragmas, "PRAGMA query_only = ON;")
	} else {
		pragmas = append(pragmas,
			"PRAGMA journal_mode = WAL;",
			"PRAGMA synchronous = NORMAL;",
			"PRAGMA wal_autocheckpoint = 1000;",
		)
	}

	for _, p := range pragmas {
		if _, err := db.ExecContext(ctx, p); err != nil {
			if !strings.Contains(err.Error(), "query_only") {
				return fmt.Errorf("exec %q: %w", p, err)
			}
		}
	}
	return nil
}

func WithImmediateTx(ctx context.Context, db *sql.DB, fn func(tx *sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelDefault})
	if err != nil {
		return fmt.Errorf("begin immediate tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit immediate tx: %w", err)
	}
	return nil
}

type RetryConfig struct {
	MaxAttempts  int
	InitialDelay time.Duration
	MaxDelay     time.Duration
}

func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts:  5,
		InitialDelay: 25 * time.Millisecond,
		MaxDelay:     500 * time.Millisecond,
	}
}

func IsBusyError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "sqlite_busy") ||
		strings.Contains(msg, "sqlite_locked") ||
		strings.Contains(msg, "busy")
}

func WithRetry(ctx context.Context, cfg RetryConfig, fn func() error) error {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 5
	}
	if cfg.InitialDelay <= 0 {
		cfg.InitialDelay = 25 * time.Millisecond
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = 500 * time.Millisecond
	}

	var err error
	delay := cfg.InitialDelay
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		err = fn()
		if err == nil {
			return nil
		}
		if !IsBusyError(err) {
			return err
		}
		if attempt == cfg.MaxAttempts {
			break
		}

		jitter := time.Duration(rng.Int63n(int64(delay / 2)))
		sleepDuration := delay + jitter
		if sleepDuration > cfg.MaxDelay {
			sleepDuration = cfg.MaxDelay
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sleepDuration):
		}

		delay *= 2
		if delay > cfg.MaxDelay {
			delay = cfg.MaxDelay
		}
	}
	return fmt.Errorf("sqlitex retry exhausted after %d attempts: %w", cfg.MaxAttempts, err)
}
