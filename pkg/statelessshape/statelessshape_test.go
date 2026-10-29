package statelessshape

import (
	"strings"
	"testing"
)

func TestDatabaseFile(t *testing.T) {
	sqlite := []byte("SQLite format 3\x00rest")
	cases := []struct {
		rel    string
		header []byte
		want   bool
	}{
		{"data/payments.db", nil, true},
		{"db/main.sqlite", nil, true},
		{"db/app.sqlite3-wal", nil, true},
		{"data/cache.db-journal", nil, true},
		{"data/pgdata/PG_VERSION", nil, true},
		{"data/mongo/WiredTiger.wt", nil, true},
		{"db/redis/dump.rdb", nil, true},
		{"data/store", sqlite, true},
		// Ordinary stateless source inside a state directory.
		{"db/migrations/20260101_init.sql", nil, false},
		{"db/schema.ts", nil, false},
		{"db/query.sql", nil, false},
		{"data/seed.json", nil, false},
		{"data/fixtures/users.csv", []byte("id,name\n"), false},
		// Outside a top-level state directory nothing is refused.
		{"src/data/app.db", nil, false},
		{"packages/worker/db/state.sqlite", sqlite, false},
		{"app.db", nil, false},
		{"data", nil, false},
	}
	for _, tc := range cases {
		if got := DatabaseFile(tc.rel, tc.header); got != tc.want {
			t.Errorf("DatabaseFile(%q) = %v, want %v", tc.rel, got, tc.want)
		}
	}
}

func TestReasonNamesFileAndDirectory(t *testing.T) {
	got := Reason("data/payments.db")
	for _, want := range []string{"data/payments.db", "inside data/", "stateless"} {
		if !strings.Contains(got, want) {
			t.Errorf("Reason = %q, missing %q", got, want)
		}
	}
}
