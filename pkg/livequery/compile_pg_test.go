package livequery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func compileFixture(t *testing.T) *pgx.Conn {
	t.Helper()
	dsn, pool := openLogicalDB(t)
	mustExec(t, pool, `
		CREATE TABLE channels (id bigint PRIMARY KEY, name text NOT NULL);
		CREATE TABLE members (channel_id bigint, user_id bigint, PRIMARY KEY (channel_id, user_id));
		CREATE TABLE messages (id bigint PRIMARY KEY, channel_id bigint, body text, created_at timestamptz);
		CREATE TABLE nokey (x int);
		CREATE VIEW v AS SELECT * FROM channels;`)
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

// adr: 20261016151947 — the compiler picks a keyed strategy where one exists and falls
// back to recompute, or refuses, everywhere else.
func TestCompileModes(t *testing.T) {
	conn := compileFixture(t)
	ctx := context.Background()
	for _, tc := range []struct {
		sql    string
		mode   Mode
		outer  bool
		reason string
	}{
		{`SELECT c.name, m.body FROM channels c JOIN messages m ON m.channel_id = c.id WHERE c.id = $1`, ModeLineage, false, ""},
		{`SELECT c.name, m.body FROM channels c LEFT JOIN messages m ON m.channel_id = c.id`, ModeLineage, true, ""},
		{`SELECT c.id, count(m.*) AS n, max(m.body) FROM channels c LEFT JOIN messages m ON m.channel_id = c.id GROUP BY c.id`, ModeGroup, true, ""},
		{`SELECT channel_id % 3 AS bucket, count(*) FROM messages GROUP BY 1 HAVING count(*) > 1`, ModeGroup, false, ""},
		{`SELECT * FROM messages ORDER BY created_at DESC LIMIT 10`, ModeLineage, false, ""},
		{`SELECT m.body, c.name FROM messages m JOIN channels c ON c.id = m.channel_id WHERE m.channel_id = $1 ORDER BY m.created_at DESC, m.body COLLATE "C" LIMIT $2 OFFSET $3`, ModeLineage, false, ""},
		{`SELECT body FROM messages LIMIT 3`, ModeLineage, false, ""},
		{`SELECT body FROM messages ORDER BY created_at`, ModeLineage, false, ""},
		{`SELECT c.name FROM channels c LEFT JOIN messages m ON m.channel_id = c.id ORDER BY m.id LIMIT 5`, ModeRecompute, false, "ORDER BY or LIMIT with outer joins"},
		{`SELECT * FROM channels WHERE id IN (SELECT channel_id FROM members) ORDER BY id LIMIT 5`, ModeRecompute, false, "ORDER BY or LIMIT with subqueries"},
		{`SELECT DISTINCT channel_id FROM messages ORDER BY 1 LIMIT 3`, ModeRecompute, false, "ORDER BY or LIMIT with grouping or DISTINCT"},
		{`SELECT body FROM messages ORDER BY id FETCH FIRST 3 ROWS WITH TIES`, ModeRecompute, false, "FETCH WITH TIES"},
		{`SELECT body FROM messages ORDER BY body USING < LIMIT 3`, ModeRecompute, false, "ORDER BY USING"},
		{`SELECT body FROM messages ORDER BY id LIMIT 2 + 3`, ModeRecompute, false, "LIMIT or OFFSET expression"},
		{`SELECT count(*), sum(id), min(body) FROM messages`, ModeGroup, false, ""},
		{`SELECT string_agg(body, ',') FROM messages`, ModeRecompute, false, "aggregate string_agg"},
		{`SELECT * FROM channels WHERE id IN (SELECT channel_id FROM members WHERE user_id = $1)`, ModeLineage, false, ""},
		{`SELECT * FROM channels c WHERE NOT EXISTS (SELECT 1 FROM messages m WHERE m.channel_id = c.id)`, ModeLineage, false, ""},
		{`SELECT * FROM channels WHERE id NOT IN (SELECT channel_id FROM members)`, ModeRecompute, false, "subquery form"},
		{`SELECT (SELECT count(*) FROM members) FROM channels`, ModeRecompute, false, "subquery outside WHERE"},
		{`SELECT DISTINCT channel_id FROM messages`, ModeGroup, false, ""},
		{`SELECT DISTINCT ON (channel_id) channel_id, body FROM messages`, ModeRecompute, false, "DISTINCT ON or DISTINCT with aggregates"},
	} {
		p, err := Compile(ctx, conn, tc.sql)
		if err != nil {
			t.Fatalf("%s: %v", tc.sql, err)
		}
		if p.Mode != tc.mode || p.HasOuterJoins() != tc.outer || p.Reason != tc.reason {
			t.Fatalf("%s: mode=%s outer=%v reason=%q", tc.sql, p.Mode, p.HasOuterJoins(), p.Reason)
		}
		if p.Mode != ModeRecompute {
			for i, sql := range p.RowRestricted {
				if p.Relations[i].Subquery {
					continue
				}
				if !strings.Contains(sql, "unnest") {
					t.Fatalf("%s: relation %d restriction missing: %s", tc.sql, i, sql)
				}
				if _, err := conn.Prepare(ctx, "", sql); err != nil {
					t.Fatalf("%s: restricted SQL does not prepare: %v\n%s", tc.sql, err, sql)
				}
			}
		}
		if p.Order != nil {
			for _, sql := range []string{p.Order.RankSQL, p.Order.OrderedSQL, p.Order.WindowSQL} {
				if _, err := conn.Prepare(ctx, "", sql); err != nil {
					t.Fatalf("%s: order SQL does not prepare: %v\n%s", tc.sql, err, sql)
				}
			}
		}
		if p.Mode == ModeGroup && p.Agg != nil {
			if _, err := conn.Prepare(ctx, "", p.Agg.FinalizeSQL); err != nil {
				t.Fatalf("%s: finalize SQL does not prepare: %v\n%s", tc.sql, err, p.Agg.FinalizeSQL)
			}
		} else if p.Mode == ModeGroup {
			if _, err := conn.Prepare(ctx, "", p.OutputRestricted); err != nil {
				t.Fatalf("%s: group-restricted SQL does not prepare: %v\n%s", tc.sql, err, p.OutputRestricted)
			}
		}
	}
	for _, sql := range []string{
		`SELECT * FROM messages WHERE created_at > now()`,
		`SELECT random() FROM messages`,
		`SELECT * FROM nokey`,
		`SELECT * FROM v`,
		`SELECT CURRENT_TIMESTAMP FROM messages`,
		`SELECT * FROM pg_class`,
		`SELECT id AS __lq_x FROM messages`,
	} {
		if _, err := Compile(ctx, conn, sql); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("%s: err = %v, want ErrUnsupported", sql, err)
		}
	}
	if _, err := conn.Exec(ctx, `CREATE DOMAIN email AS text; CREATE TABLE people (id int PRIMARY KEY, mail email, tags int[])`); err != nil {
		t.Fatal(err)
	}
	cols, err := Compile(ctx, conn, `SELECT count(*), sum(id), min(body), max(created_at) FROM messages`)
	if err != nil {
		t.Fatal(err)
	}
	people, err := Compile(ctx, conn, `SELECT id, mail, tags FROM people`)
	if err != nil {
		t.Fatal(err)
	}
	got := fmt.Sprint(cols.Columns, people.Columns)
	if want := "[{count int8} {sum numeric} {min text} {max timestamptz}] [{id int4} {mail text} {tags _int4}]"; got != want {
		t.Fatalf("columns = %s, want %s", got, want)
	}
	feed, err := Compile(ctx, conn, `SELECT m.body FROM messages m JOIN channels c ON c.id = m.channel_id ORDER BY m.body COLLATE "C" DESC, c.name LIMIT $1 OFFSET 2`)
	if err != nil {
		t.Fatal(err)
	}
	var items []string
	for _, it := range feed.Order.Items {
		items = append(items, fmt.Sprintf("%s/%s/%v/%v", it.Type, it.Collation, it.Desc, it.NullsFirst))
	}
	if got, want := strings.Join(items, " "), `text/"C"/true/true text/"default"/false/false bigint//false/false bigint//false/false`; got != want {
		t.Fatalf("order items = %s, want %s", got, want)
	}
	if feed.Order.Limit != (Bound{Set: true, Param: 1}) || feed.Order.Offset != (Bound{Set: true, Value: 2}) || !feed.Order.Ordered {
		t.Fatalf("bounds %+v %+v", feed.Order.Limit, feed.Order.Offset)
	}
	p, err := Compile(ctx, conn, `SELECT c.name FROM channels c LEFT JOIN messages m ON m.channel_id = c.id LEFT JOIN members x ON x.channel_id = c.id`)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Relations[1].Preserved; len(got) != 1 || got[0] != 0 {
		t.Fatalf("messages preserved = %v, want [0]", got)
	}
	if got := p.Relations[2].Preserved; len(got) != 2 {
		t.Fatalf("members preserved = %v, want channels and messages", got)
	}
}
