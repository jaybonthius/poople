package app_test

import (
	"context"
	"testing"

	"poople/app"
	"poople/ladder"
	"poople/store"
	"poople/store/zz"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func count(t *testing.T, db *store.Store, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.Read(t.Context(), func(conn *sqlite.Conn) error {
		return sqlitex.Execute(conn, query, &sqlitex.ExecOptions{Args: args, ResultFunc: func(stmt *sqlite.Stmt) error {
			n = stmt.ColumnInt64(0)
			return nil
		}})
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestOpenStoresWordDataOnce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for range 2 {
		backend, err := app.Open(context.Background(), dir)
		if err != nil {
			t.Fatal(err)
		}
		db := backend.Database()
		for _, c := range []struct {
			name, query string
			want        int64
		}{
			{"versions", "SELECT count(*) FROM word_lists", 1},
			{"words", "SELECT count(*) FROM words", 5662},
			{"reachable", "SELECT count(*) FROM words WHERE distance IS NOT NULL", 5586},
			{"common start pool", "SELECT count(*) FROM words WHERE common = 1 AND distance >= 4", 2201},
			{"calendar", "SELECT count(*) FROM calendar_entries", 1092},
			{"boop", "SELECT count(*) FROM words WHERE word = 'boop'", 0},
			{"tips distance", "SELECT distance FROM words WHERE word = 'tips'", 4},
			{"calendar 0 is dawn", "SELECT count(*) FROM calendar_entries WHERE position = 0 AND word = 'dawn'", 1},
		} {
			if got := count(t, db, c.query); got != c.want {
				t.Errorf("%s = %d, want %d", c.name, got, c.want)
			}
		}
		if err := backend.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadWordsAddsChangedDataAsNewVersion(t *testing.T) {
	t.Parallel()
	backend, err := app.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { backend.Close() })
	db := backend.Database()
	csw, err := ladder.Load()
	if err != nil {
		t.Fatal(err)
	}
	first, err := app.LoadWords(t.Context(), db, csw)
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := ladder.Parse([]byte("pads\npals\npods\npoop\npoos\nzzzz\n"), []byte("poos\npals\n"), []byte("pals\n"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.LoadWords(t.Context(), db, fixture)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := app.LoadWords(t.Context(), db, fixture); err != nil || again != second {
		t.Fatalf("reloading the fixture = %d, %v; want %d", again, err, second)
	}
	if second <= first {
		t.Fatalf("new version id %d not after %d", second, first)
	}
	if got := count(t, db, "SELECT count(*) FROM words WHERE list_id = ?", first); got != 5662 {
		t.Fatalf("first version words = %d", got)
	}
	if got := count(t, db, "SELECT count(*) FROM words WHERE list_id = ?", second); got != 6 {
		t.Fatalf("second version words = %d", got)
	}
	if got := count(t, db, "SELECT count(*) FROM words WHERE list_id = ? AND word = 'zzzz' AND distance IS NULL", second); got != 1 {
		t.Fatal("unreachable word lacks NULL distance")
	}
	if got := count(t, db, "SELECT count(*) FROM calendar_entries WHERE list_id = ? AND position = 0 AND word = 'pals'", second); got != 1 {
		t.Fatal("calendar kept a start word closer than MinStartDistance or lost pals")
	}
	if got := count(t, db, "SELECT count(*) FROM calendar_entries WHERE list_id = ?", second); got != 1 {
		t.Fatalf("second calendar entries = %d", got)
	}
}

func TestLoadWordsReactivatesRevertedData(t *testing.T) {
	t.Parallel()
	backend, err := app.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { backend.Close() })
	db := backend.Database()
	csw, err := ladder.Load()
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := ladder.Parse([]byte("pads\npals\npods\npoop\npoos\n"), []byte("pals\n"), []byte("pals\n"))
	if err != nil {
		t.Fatal(err)
	}
	current := func() int64 {
		var id int64
		if err := db.Read(t.Context(), func(conn *sqlite.Conn) (err error) {
			id, err = zz.OnceCurrentWordList(conn)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	// A -> B -> A -> B, each step within the same millisecond or not.
	a, err := app.LoadWords(t.Context(), db, csw)
	if err != nil || current() != a {
		t.Fatalf("A = %d, %v; current %d", a, err, current())
	}
	b, err := app.LoadWords(t.Context(), db, fixture)
	if err != nil || b == a || current() != b {
		t.Fatalf("B = %d, %v; current %d", b, err, current())
	}
	for _, step := range []struct {
		data *ladder.Data
		want int64
	}{{csw, a}, {fixture, b}, {csw, a}} {
		got, err := app.LoadWords(t.Context(), db, step.data)
		if err != nil || got != step.want || current() != step.want {
			t.Fatalf("reverting to %d = %d, %v; current %d", step.want, got, err, current())
		}
	}
	if got := count(t, db, "SELECT count(*) FROM word_lists"); got != 2 {
		t.Fatalf("versions = %d, want reverted data reused", got)
	}
}
