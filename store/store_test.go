package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"poople/store/zz"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func testStore(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func scalar(t *testing.T, s *Store, query string) string {
	t.Helper()
	var result string
	if err := s.Read(t.Context(), func(conn *sqlite.Conn) error {
		return sqlitex.Execute(conn, query, &sqlitex.ExecOptions{ResultFunc: func(stmt *sqlite.Stmt) error { result = stmt.ColumnText(0); return nil }})
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestOpenMigratesAndConfiguresConnections(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "nested", "data")
	s := testStore(t, dir)
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("data directory not created privately: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, Filename)); err != nil {
		t.Fatal(err)
	}
	var app string
	if err := s.Read(t.Context(), func(conn *sqlite.Conn) (err error) {
		app, err = zz.OnceGetAppMeta(conn, "app")
		return err
	}); err != nil || app != "poople" {
		t.Fatalf("migrated app_meta=%q, %v", app, err)
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	// Count applied versions rather than take the highest, so gaps in numbering pass.
	if got, want := scalar(t, s, "SELECT count(DISTINCT version_id) FROM goose_db_version WHERE version_id > 0"), strconv.Itoa(len(entries)); got != want {
		t.Fatalf("applied migrations=%s, want %s", got, want)
	}
	if got := scalar(t, s, "PRAGMA journal_mode"); got != "wal" {
		t.Fatalf("read connection journal_mode=%s", got)
	}
	if err := s.db.WriteWithoutTx(t.Context(), checkPragmas); err != nil {
		t.Fatal(err)
	}
}

func TestWriteCommitsOrRollsBackAndSurvivesReopen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	insert := func(key string) func(*sqlite.Conn) error {
		return func(conn *sqlite.Conn) error {
			return sqlitex.Execute(conn, "INSERT INTO app_meta(key, value) VALUES (?1, 'fixture')", &sqlitex.ExecOptions{Args: []any{key}})
		}
	}
	if err := s.Write(t.Context(), insert("committed")); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("fixture failure")
	if err := s.Write(t.Context(), func(conn *sqlite.Conn) error {
		if err := insert("rolled-back")(conn); err != nil {
			return err
		}
		return failure
	}); !errors.Is(err, failure) {
		t.Fatalf("failed write error=%v", err)
	}
	if err := s.Write(t.Context(), insert("committed")); err == nil {
		t.Fatal("duplicate key committed")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = testStore(t, dir)
	if got := scalar(t, s, "SELECT group_concat(key, ',') FROM (SELECT key FROM app_meta ORDER BY key)"); got != "app,committed" {
		t.Fatalf("keys after reopen=%s", got)
	}
	if got := scalar(t, s, "SELECT count(*) FROM goose_db_version WHERE version_id = 1"); got != "1" {
		t.Fatalf("reopen reapplied migrations: %s", got)
	}
}

func TestReadRefusesWrites(t *testing.T) {
	t.Parallel()
	s := testStore(t, t.TempDir())
	for range 2 * runtime.NumCPU() { // reach every pooled read connection
		if err := s.Read(t.Context(), func(conn *sqlite.Conn) error {
			return sqlitex.Execute(conn, "INSERT INTO app_meta(key, value) VALUES ('read', 'write')", nil)
		}); err == nil {
			t.Fatal("read connection accepted a write")
		}
	}
	if got := scalar(t, s, "SELECT count(*) FROM app_meta WHERE key = 'read'"); got != "0" {
		t.Fatal("read connection wrote a row")
	}
}

func TestOpenRejectsMissingOrAmbiguousDirectoryAndCancellation(t *testing.T) {
	t.Parallel()
	if _, err := Open(t.Context(), " "); err == nil {
		t.Fatal("blank data directory accepted")
	}
	for _, name := range []string{"query?mode=memory", "frag#ment", "per%41cent"} {
		dir := filepath.Join(t.TempDir(), name)
		if s, err := Open(t.Context(), dir); err == nil {
			s.Close()
			t.Fatalf("URI-ambiguous path accepted: %s", name)
		} else if !strings.Contains(err.Error(), "must not contain") {
			t.Fatal(err)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("rejected path was created")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	dir := filepath.Join(t.TempDir(), "not-created")
	if _, err := Open(ctx, dir); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled open=%v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("cancelled open created storage")
	}
}

func TestOpenMakesAnExistingDataDirectoryAndDatabasePrivate(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil { // the umask may have narrowed Mkdir
		t.Fatal(err)
	}
	testStore(t, dir)
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("data directory mode = %v, %v; want 0700", info.Mode().Perm(), err)
	}
	for _, name := range []string{Filename, Filename + "-wal"} {
		if info, err := os.Stat(filepath.Join(dir, name)); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v, %v; want 0600", name, info.Mode().Perm(), err)
		}
	}
}

func TestOpenMakesAnExistingDatabaseFilePrivate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, Filename)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	testStore(t, dir)
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("database mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
}
