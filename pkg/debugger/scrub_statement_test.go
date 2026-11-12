package debugger

import "testing"

// The scrubber missed backslash-escaped quotes and JSON string values, so
// MySQL and Mongo-style statements kept customer values.
func TestScrubStatementLiterals(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`SELECT * FROM users WHERE email = 'a@b.c' AND id = 42`, `SELECT * FROM users WHERE email = ? AND id = ?`},
		{`SELECT 'it''s'`, `SELECT ?`},
		{`SELECT * FROM t WHERE name = 'O\'Brien' AND k = 'x'`, `SELECT * FROM t WHERE name = ? AND k = ?`},
		{`db.users.find({"email": "alice@example.com", "age": 31})`, `db.users.find({"email": ?, "age": ?})`},
		{`SELECT "user_id" FROM "public"."orders"`, `SELECT "user_id" FROM "public"."orders"`},
		{`SELECT id FROM orders WHERE id = $1 LIMIT 10`, `SELECT id FROM orders WHERE id = $1 LIMIT ?`},
		{`SELECT price FROM t1 WHERE price > 9.99`, `SELECT price FROM t1 WHERE price > ?`},
		// PostgreSQL dollar quoting; positional parameters stay.
		{`SELECT $$secret token$$, $body$it's 'here'$body$ WHERE a = $2`, `SELECT ?, ? WHERE a = $2`},
		// MySQL double-quoted strings where only a value can stand.
		{`SELECT * FROM users WHERE email = "bob@x.com" AND name LIKE "Bo%"`, `SELECT * FROM users WHERE email = ? AND name LIKE ?`},
		{`SELECT id FROM users WHERE email IN ("a@x.com", "b@x.com")`, `SELECT id FROM users WHERE email IN (?, ?)`},
		{`INSERT INTO "users" ("email", "age") VALUES ("bob@x.com", 30), ("al@x.com", 41)`, `INSERT INTO "users" ("email", "age") VALUES (?, ?), (?, ?)`},
		// Hex literals.
		{`SELECT * FROM t WHERE token = 0xDEADBEEF`, `SELECT * FROM t WHERE token = ?`},
		// JSON documents: array members are values too; keys stay.
		{`{"find": "users", "filter": {"email": {"$in": ["a@x.com", "b@x.com"]}}}`, `{"find": ?, "filter": {"email": {"$in": [?, ?]}}}`},
		{`db.users.find({"tags": ["vip", "beta"]})`, `db.users.find({"tags": [?, ?]})`},
	} {
		if got := ScrubStatementLiterals(tc.in); got != tc.want {
			t.Errorf("ScrubStatementLiterals(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Command stores record arguments unquoted, so only the command survives.
func TestScrubStatementForSystem(t *testing.T) {
	for _, tc := range []struct{ system, in, want string }{
		{"redis", "SET session:abc123 eyJhbGciOi", "SET ? ?"},
		{"Redis", "auth hunter2", "AUTH ?"},
		{"memcached", "get user:42", "GET ?"},
		{"postgresql", "SELECT * FROM t WHERE id = 7", "SELECT * FROM t WHERE id = ?"},
		{"redis", "", ""},
	} {
		if got := ScrubStatementForSystem(tc.in, tc.system); got != tc.want {
			t.Errorf("ScrubStatementForSystem(%q, %q) = %q, want %q", tc.in, tc.system, got, tc.want)
		}
	}
}
