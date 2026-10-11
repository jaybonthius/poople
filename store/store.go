// Package store owns poople.sqlite: migrations, the connection pools and transactions.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	toolbeltdb "github.com/delaneyj/toolbelt/db"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// Filename is the database inside the data directory.
const Filename = "poople.sqlite"

const busyTimeoutMS = 10000

//go:embed migrations/*.sql
var migrations embed.FS

// Store has one write connection and a CPU-sized read pool. Close after all users stop.
type Store struct {
	db *toolbeltdb.Database
}

// Open migrates poople.sqlite in dataDir, then opens the runtime connections.
func Open(ctx context.Context, dataDir string) (_ *Store, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("an explicit data directory is required")
	}
	dir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, Filename)
	// Both drivers open the path as a URI, where these characters change its meaning.
	if strings.ContainsAny(path, "?#%") {
		return nil, fmt.Errorf("data directory path must not contain '?', '#' or '%%': %s", dir)
	}
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err = migrate(ctx, path); err != nil {
		return nil, err
	}
	db, err := toolbeltdb.NewDatabase(ctx,
		toolbeltdb.DatabaseWithFilename(path),
		toolbeltdb.DatabaseWithPragmas(
			fmt.Sprintf("busy_timeout = %d", busyTimeoutMS),
			"foreign_keys = ON",
			"synchronous = NORMAL",
		),
	)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, db.Close())
		}
	}()
	if err = db.WriteWithoutTx(ctx, checkPragmas); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

// migrate runs the embedded Goose migrations on its own connection and closes it.
func migrate(ctx context.Context, path string) (err error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	defer func() { err = errors.Join(err, db.Close()) }()
	for _, pragma := range []string{"journal_mode = WAL", "foreign_keys = ON", fmt.Sprintf("busy_timeout = %d", busyTimeoutMS)} {
		if _, err = db.ExecContext(ctx, "PRAGMA "+pragma); err != nil {
			return fmt.Errorf("migration PRAGMA %s: %w", pragma, err)
		}
	}
	migrationFS, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, migrationFS)
	if err != nil {
		return err
	}
	if _, err = provider.Up(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// checkPragmas reads back the write connection's settings.
func checkPragmas(conn *sqlite.Conn) error {
	for _, setting := range []struct{ pragma, want string }{
		{"journal_mode", "wal"}, {"synchronous", "1"}, {"foreign_keys", "1"}, {"busy_timeout", fmt.Sprint(busyTimeoutMS)},
	} {
		var got string
		if err := sqlitex.ExecuteTransient(conn, "PRAGMA "+setting.pragma, &sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error { got = stmt.ColumnText(0); return nil },
		}); err != nil {
			return err
		}
		if got != setting.want {
			return fmt.Errorf("PRAGMA %s = %s, want %s", setting.pragma, got, setting.want)
		}
	}
	return nil
}

// Read runs query in a read transaction on a pooled read-only connection.
// The callback runs bounded SQL only and must not retain SQL handles.
func (s *Store) Read(ctx context.Context, query func(*sqlite.Conn) error) error {
	if query == nil {
		return errors.New("read requires a callback")
	}
	return s.db.ReadTX(ctx, func(conn *sqlite.Conn) error {
		// Read connections only ever read; a write through one is a bug.
		if err := sqlitex.ExecuteTransient(conn, "PRAGMA query_only = ON", nil); err != nil {
			return err
		}
		return query(conn)
	})
}

// Write runs change in one immediate transaction on the single write connection,
// committing when it returns nil. The callback runs bounded SQL only: no parsing,
// hashing, network calls or rendering, and no transaction control.
func (s *Store) Write(ctx context.Context, change func(*sqlite.Conn) error) error {
	if change == nil {
		return errors.New("write requires a callback")
	}
	return s.db.WriteTX(ctx, change)
}

// Close releases both pools.
func (s *Store) Close() error {
	return s.db.Close()
}
