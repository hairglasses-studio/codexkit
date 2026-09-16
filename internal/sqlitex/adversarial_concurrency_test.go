package sqlitex

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func TestAdversarial_WithImmediateTx_MultiProcessContention(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "adversarial_contention.db")

	initDB, err := Open("", dbPath, DefaultOptions())
	if err != nil {
		t.Fatalf("Open init failed: %v", err)
	}
	if _, err := initDB.Exec("CREATE TABLE ledger (id INTEGER PRIMARY KEY AUTOINCREMENT, proc_id INT, val INT, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP);"); err != nil {
		t.Fatalf("init table failed: %v", err)
	}
	initDB.Close()

	const numWriters = 10
	const txPerWriter = 40
	const numReaders = 5

	writerDBs := make([]*sql.DB, numWriters)
	for i := range numWriters {
		db, err := Open("", dbPath, DefaultOptions())
		if err != nil {
			t.Fatalf("Open writer %d failed: %v", i, err)
		}
		defer db.Close()
		writerDBs[i] = db
	}

	readerDBs := make([]*sql.DB, numReaders)
	for i := range numReaders {
		db, err := Open("", dbPath, ReadOnlyOptions())
		if err != nil {
			t.Fatalf("Open reader %d failed: %v", i, err)
		}
		defer db.Close()
		readerDBs[i] = db
	}

	ctx := context.Background()
	var writerWg sync.WaitGroup
	var readerWg sync.WaitGroup
	errCh := make(chan error, numWriters*txPerWriter+100)

	for w := range numWriters {
		writerWg.Add(1)
		go func(procID int, db *sql.DB) {
			defer writerWg.Done()
			for i := range txPerWriter {
				retryErr := WithRetry(ctx, DefaultRetryConfig(), func() error {
					return WithImmediateTx(ctx, db, func(tx *sql.Tx) error {
						_, err := tx.Exec("INSERT INTO ledger (proc_id, val) VALUES (?, ?)", procID, i)
						return err
					})
				})
				if retryErr != nil {
					errCh <- fmt.Errorf("writer %d iter %d failed: %w", procID, i, retryErr)
					return
				}
			}
		}(w, writerDBs[w])
	}

	stopReaders := make(chan struct{})
	for r := range numReaders {
		readerWg.Add(1)
		go func(readerID int, db *sql.DB) {
			defer readerWg.Done()
			for {
				select {
				case <-stopReaders:
					return
				default:
					var count int
					if err := db.QueryRow("SELECT count(*) FROM ledger").Scan(&count); err != nil {
						errCh <- fmt.Errorf("reader %d failed: %w", readerID, err)
						return
					}
				}
			}
		}(r, readerDBs[r])
	}

	writerWg.Wait()
	close(stopReaders)
	readerWg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrency error: %v", err)
	}

	verifyDB, err := Open("", dbPath, DefaultOptions())
	if err != nil {
		t.Fatalf("Open verify failed: %v", err)
	}
	defer verifyDB.Close()

	var totalCount int
	if err := verifyDB.QueryRow("SELECT count(*) FROM ledger").Scan(&totalCount); err != nil {
		t.Fatalf("query total rows failed: %v", err)
	}

	expected := numWriters * txPerWriter
	if totalCount != expected {
		t.Fatalf("expected %d rows inserted, got %d", expected, totalCount)
	}

	var integrity string
	if err := verifyDB.QueryRow("PRAGMA integrity_check;").Scan(&integrity); err != nil {
		t.Fatalf("integrity check failed: %v", err)
	}
	if integrity != "ok" {
		t.Fatalf("corrupted database: %s", integrity)
	}
}
