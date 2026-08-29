package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const DatabaseFileName = "catalog.sqlite3"

type Catalog struct {
	db             *sql.DB
	path           string
	quarantinePath string
}

func Path(portableRoot string) string {
	return filepath.Join(portableRoot, "data", DatabaseFileName)
}

func Open(ctx context.Context, portableRoot string) (*Catalog, error) {
	if strings.TrimSpace(portableRoot) == "" {
		return nil, errors.New("portable root is required")
	}
	dataDir := filepath.Join(portableRoot, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create catalog data directory: %w", err)
	}
	return OpenPath(ctx, filepath.Join(dataDir, DatabaseFileName))
}

func OpenPath(ctx context.Context, path string) (*Catalog, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("catalog path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create catalog directory: %w", err)
	}
	_, statErr := os.Stat(path)
	existed := statErr == nil
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, fmt.Errorf("stat catalog: %w", statErr)
	}

	db, err := openDatabase(ctx, path)
	if err != nil {
		if !existed {
			return nil, err
		}
		return quarantineAndReopen(ctx, path, nil, err)
	}
	if existed {
		if err := checkIntegrity(ctx, db); err != nil {
			return quarantineAndReopen(ctx, path, db, err)
		}
	}
	if err := ensureSchema(ctx, db); err != nil {
		if errors.Is(err, ErrCatalogCorrupt) {
			return quarantineAndReopen(ctx, path, db, err)
		}
		_ = db.Close()
		return nil, err
	}
	if err := checkIntegrity(ctx, db); err != nil {
		return quarantineAndReopen(ctx, path, db, err)
	}
	return &Catalog{db: db, path: path}, nil
}

func openDatabase(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite catalog: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite catalog: %w", err)
	}
	for _, statement := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = FULL",
		"PRAGMA busy_timeout = 5000",
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("configure sqlite catalog with %q: %w", statement, err)
		}
	}
	return db, nil
}

func checkIntegrity(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA quick_check")
	if err != nil {
		return fmt.Errorf("%w: quick_check query: %v", ErrCatalogCorrupt, err)
	}
	defer rows.Close()
	seen := false
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			return fmt.Errorf("%w: quick_check scan: %v", ErrCatalogCorrupt, err)
		}
		seen = true
		if result != "ok" {
			return fmt.Errorf("%w: quick_check: %s", ErrCatalogCorrupt, result)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("%w: quick_check rows: %v", ErrCatalogCorrupt, err)
	}
	if !seen {
		return fmt.Errorf("%w: quick_check returned no result", ErrCatalogCorrupt)
	}
	return nil
}

func quarantineAndReopen(ctx context.Context, path string, db *sql.DB, cause error) (*Catalog, error) {
	if db != nil {
		_ = db.Close()
	}
	quarantinePath, err := quarantine(path)
	if err != nil {
		return nil, fmt.Errorf("quarantine corrupt catalog after %v: %w", cause, err)
	}
	fresh, err := openDatabase(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("open clean catalog after quarantining %s: %w", quarantinePath, err)
	}
	if err := ensureSchema(ctx, fresh); err != nil {
		_ = fresh.Close()
		return nil, fmt.Errorf("initialize clean catalog after quarantining %s: %w", quarantinePath, err)
	}
	if err := checkIntegrity(ctx, fresh); err != nil {
		_ = fresh.Close()
		return nil, fmt.Errorf("validate clean catalog after quarantining %s: %w", quarantinePath, err)
	}
	return &Catalog{db: fresh, path: path, quarantinePath: quarantinePath}, nil
}

func quarantine(path string) (string, error) {
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	quarantinePath := path + ".corrupt-" + stamp
	if err := os.Rename(path, quarantinePath); err != nil {
		return "", fmt.Errorf("move corrupt catalog: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		oldSidecar := path + suffix
		newSidecar := quarantinePath + suffix
		if err := os.Rename(oldSidecar, newSidecar); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("move corrupt catalog sidecar %s: %w", suffix, err)
		}
	}
	return quarantinePath, nil
}

func (c *Catalog) DB() *sql.DB {
	if c == nil {
		return nil
	}
	return c.db
}

func (c *Catalog) Path() string {
	if c == nil {
		return ""
	}
	return c.path
}

func (c *Catalog) QuarantinePath() string {
	if c == nil {
		return ""
	}
	return c.quarantinePath
}

func (c *Catalog) CheckIntegrity(ctx context.Context) error {
	if c == nil || c.db == nil {
		return errors.New("catalog is closed")
	}
	return checkIntegrity(ctx, c.db)
}

func (c *Catalog) Close() error {
	if c == nil || c.db == nil {
		return nil
	}
	err := c.db.Close()
	c.db = nil
	return err
}
