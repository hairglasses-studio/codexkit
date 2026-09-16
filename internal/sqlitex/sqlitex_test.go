package sqlitex

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpenDefaultOptions(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	opts := DefaultOptions()
	db, err := Open("", dbPath, opts)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode;").Scan(&journalMode); err != nil {
		t.Fatalf("query journal_mode failed: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("expected wal journal_mode, got %s", journalMode)
	}

	var busyTimeout int
	if err := db.QueryRow("PRAGMA busy_timeout;").Scan(&busyTimeout); err != nil {
		t.Fatalf("query busy_timeout failed: %v", err)
	}
	if busyTimeout != 5000 {
		t.Fatalf("expected busy_timeout 5000, got %d", busyTimeout)
	}

	var synchronous int
	if err := db.QueryRow("PRAGMA synchronous;").Scan(&synchronous); err != nil {
		t.Fatalf("query synchronous failed: %v", err)
	}
	if synchronous != 1 {
		t.Fatalf("expected synchronous NORMAL (1), got %d", synchronous)
	}

	var foreignKeys int
	if err := db.QueryRow("PRAGMA foreign_keys;").Scan(&foreignKeys); err != nil {
		t.Fatalf("query foreign_keys failed: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("expected foreign_keys ON (1), got %d", foreignKeys)
	}
}

func TestOpenReadOnly(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "ro.db")

	wDB, err := Open("", dbPath, DefaultOptions())
	if err != nil {
		t.Fatalf("Open writer failed: %v", err)
	}
	if _, err := wDB.Exec("CREATE TABLE t (id INT);"); err != nil {
		t.Fatalf("create table failed: %v", err)
	}
	if err := wDB.Close(); err != nil {
		t.Fatalf("close writer failed: %v", err)
	}

	roDB, err := Open("", dbPath, ReadOnlyOptions())
	if err != nil {
		t.Fatalf("Open reader failed: %v", err)
	}
	defer roDB.Close()

	var count int
	if err := roDB.QueryRow("SELECT count(*) FROM t").Scan(&count); err != nil {
		t.Fatalf("read failed: %v", err)
	}

	if _, err := roDB.Exec("INSERT INTO t VALUES (1)"); err == nil {
		t.Fatal("expected insert on readonly db to fail, but succeeded")
	}
}

func TestWithImmediateTxCommitAndRollback(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "tx.db")

	db, err := Open("", dbPath, DefaultOptions())
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec("CREATE TABLE counter (val INT); INSERT INTO counter VALUES (0);"); err != nil {
		t.Fatalf("init table failed: %v", err)
	}

	ctx := context.Background()
	err = WithImmediateTx(ctx, db, func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE counter SET val = val + 10")
		return err
	})
	if err != nil {
		t.Fatalf("WithImmediateTx commit failed: %v", err)
	}

	var val int
	if err := db.QueryRow("SELECT val FROM counter").Scan(&val); err != nil {
		t.Fatalf("query counter failed: %v", err)
	}
	if val != 10 {
		t.Fatalf("expected val 10, got %d", val)
	}

	expectedErr := errors.New("simulated error")
	err = WithImmediateTx(ctx, db, func(tx *sql.Tx) error {
		if _, err := tx.Exec("UPDATE counter SET val = val + 50"); err != nil {
			return err
		}
		return expectedErr
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected simulated error, got %v", err)
	}

	if err := db.QueryRow("SELECT val FROM counter").Scan(&val); err != nil {
		t.Fatalf("query counter failed: %v", err)
	}
	if val != 10 {
		t.Fatalf("expected val to remain 10 after rollback, got %d", val)
	}
}

func TestWithImmediateTxConcurrentWriters(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "concurrent.db")

	db, err := Open("", dbPath, DefaultOptions())
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec("CREATE TABLE records (id INTEGER PRIMARY KEY AUTOINCREMENT, worker INT, iter INT);"); err != nil {
		t.Fatalf("create table failed: %v", err)
	}

	workers := 8
	iterations := 25
	ctx := context.Background()

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				retryErr := WithRetry(ctx, DefaultRetryConfig(), func() error {
					return WithImmediateTx(ctx, db, func(tx *sql.Tx) error {
						_, err := tx.Exec("INSERT INTO records (worker, iter) VALUES (?, ?)", workerID, i)
						return err
					})
				})
				if retryErr != nil {
					t.Errorf("worker %d iter %d failed: %v", workerID, i, retryErr)
				}
			}
		}(w)
	}

	wg.Wait()

	var count int
	if err := db.QueryRow("SELECT count(*) FROM records").Scan(&count); err != nil {
		t.Fatalf("count failed: %v", err)
	}
	expected := workers * iterations
	if count != expected {
		t.Fatalf("expected %d rows, got %d", expected, count)
	}
}

func TestWithRetrySuccessAfterBusy(t *testing.T) {
	ctx := context.Background()
	cfg := RetryConfig{
		MaxAttempts:  5,
		InitialDelay: 5 * time.Millisecond,
		MaxDelay:     50 * time.Millisecond,
	}

	var attempts int32
	err := WithRetry(ctx, cfg, func() error {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			return errors.New("sqlite_busy: database is locked")
		}
		return nil
	})

	if err != nil {
		t.Fatalf("expected WithRetry to succeed, got %v", err)
	}
	if atomic.LoadInt32(&attempts) != 3 {
		t.Fatalf("expected 3 attempts, got %d", atomic.LoadInt32(&attempts))
	}
}

func TestWithRetryExhaustion(t *testing.T) {
	ctx := context.Background()
	cfg := RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 5 * time.Millisecond,
		MaxDelay:     20 * time.Millisecond,
	}

	var attempts int32
	err := WithRetry(ctx, cfg, func() error {
		atomic.AddInt32(&attempts, 1)
		return errors.New("database is locked")
	})

	if err == nil {
		t.Fatal("expected error on retry exhaustion, got nil")
	}
	if atomic.LoadInt32(&attempts) != 3 {
		t.Fatalf("expected 3 attempts, got %d", atomic.LoadInt32(&attempts))
	}
}

func TestWithRetryNonBusyError(t *testing.T) {
	ctx := context.Background()
	cfg := DefaultRetryConfig()

	customErr := errors.New("unrelated syntax error")
	var attempts int32
	err := WithRetry(ctx, cfg, func() error {
		atomic.AddInt32(&attempts, 1)
		return customErr
	})

	if !errors.Is(err, customErr) {
		t.Fatalf("expected immediate non-busy error, got %v", err)
	}
	if atomic.LoadInt32(&attempts) != 1 {
		t.Fatalf("expected 1 attempt for non-busy error, got %d", atomic.LoadInt32(&attempts))
	}
}
