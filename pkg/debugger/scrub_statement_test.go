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
	} {
		if got := ScrubStatementLiterals(tc.in); got != tc.want {
			t.Errorf("ScrubStatementLiterals(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
