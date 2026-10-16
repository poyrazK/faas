package livequery

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// The differential oracle. Engine.VerifyEachRound runs these checks inside
// the round transaction, so PostgreSQL answers at the same snapshot the
// engine maintained.

// checkVisibility compares the engine's visibility verdict for a decoded
// transaction with PostgreSQL's own.
func checkVisibility(ctx context.Context, tx pgx.Tx, snap Snapshot, xid uint64) error {
	var pgVisible bool
	if err := tx.QueryRow(ctx, "SELECT pg_visible_in_snapshot($1::text::xid8, pg_current_snapshot())",
		strconv.FormatUint(xid, 10)).Scan(&pgVisible); err != nil {
		return fmt.Errorf("check visibility: %w", err)
	}
	if pgVisible != snap.Visible(xid) {
		return fmt.Errorf("xid %d visibility %v, PostgreSQL says %v (snapshot %s)", xid, snap.Visible(xid), pgVisible, snap)
	}
	return nil
}

// verify compares the member's caches and result with the query run at the
// round snapshot with the member's arguments.
func (m *member) verify(ctx context.Context, tx pgx.Tx) error {
	p := m.group.plan
	if m.join != nil {
		if err := m.verifyKeyed(ctx, tx, "join lineage", p.JoinSQL, m.join, false); err != nil {
			return err
		}
	}
	if m.input != nil {
		if err := m.verifyKeyed(ctx, tx, "group input", p.RowSQL, m.input, true); err != nil {
			return err
		}
	}
	for s, sq := range p.Subqueries {
		if err := m.verifyKeyed(ctx, tx, fmt.Sprintf("subquery %d pairs", s), sq.SQL, m.semi[s], false); err != nil {
			return err
		}
	}
	_, rows, err := queryText(ctx, tx, p.SQL, m.args)
	if err != nil {
		return fmt.Errorf("oracle query: %w", err)
	}
	want := map[string]int{}
	for _, values := range rows {
		want[encodeValues(values)]++
	}
	got := map[string]int{}
	snap := m.snapshotRows()
	for _, r := range snap {
		got[r.enc]++
	}
	for enc, n := range want {
		if got[enc] != n {
			return fmt.Errorf("divergence (mode %s): row %q expected %d times, maintained %d times", p.Mode, printable(enc), n, got[enc])
		}
	}
	for enc, n := range got {
		if want[enc] != n {
			var lineage []string
			for _, r := range snap {
				if r.enc == enc {
					lineage = r.lineage
				}
			}
			return fmt.Errorf("divergence (mode %s): row %q (lineage %q) maintained %d times, expected %d times",
				p.Mode, printable(enc), lineage, n, want[enc])
		}
	}
	return nil
}

// verifyKeyed compares a lineage-keyed cache with its statement at the
// snapshot.
func (m *member) verifyKeyed(ctx context.Context, tx pgx.Tx, layer, sql string, set *keyedSet, groupValues bool) error {
	g := m.group
	byMember, _, err := g.readKeyed(ctx, tx, sql, []*member{m}, nil, groupValues)
	if err != nil {
		return fmt.Errorf("oracle %s: %w", layer, err)
	}
	fresh := byMember[m]
	for id, r := range fresh {
		got, ok := set.rows[id]
		if !ok {
			return fmt.Errorf("divergence in %s: missing row lineage %q values %q", layer, r.lineage, printable(r.enc))
		}
		if got.enc != r.enc {
			return fmt.Errorf("divergence in %s: row lineage %q values %q, expected %q", layer, r.lineage, printable(got.enc), printable(r.enc))
		}
	}
	for id, r := range set.rows {
		if _, ok := fresh[id]; ok {
			continue
		}
		var now []string
		for i, key := range r.lineage {
			if key == "" || len(g.plan.JoinRestricted) == 0 {
				continue
			}
			rows, _, err := g.readKeyed(ctx, tx, g.plan.JoinRestricted[i], []*member{m},
				keyArrays(map[string]struct{}{key: {}}, len(g.plan.Relations[i].Keys)), false)
			if err != nil {
				now = append(now, fmt.Sprintf("r%d error: %v", i, err))
			}
			for _, nr := range rows[m] {
				now = append(now, fmt.Sprintf("r%d:%q", i, nr.lineage))
			}
		}
		return fmt.Errorf("divergence in %s: stale row lineage %q values %q; at snapshot these keys join as %v; round keys %s; expanded %s",
			layer, r.lineage, printable(r.enc), now, describeKeys(g.lastKeys), describeKeys(g.lastExpanded))
	}
	return nil
}

func describeKeys(keys []map[string]struct{}) string {
	var parts []string
	for i, set := range keys {
		var ks []string
		for k := range set {
			ks = append(ks, strings.ReplaceAll(k, keySep, "|"))
		}
		sort.Strings(ks)
		parts = append(parts, fmt.Sprintf("r%d=%v", i, ks))
	}
	return strings.Join(parts, " ")
}

func printable(enc string) string {
	out := []byte{}
	for i := 0; i < len(enc); i++ {
		switch {
		case enc[i] == 0 && i+1 < len(enc) && enc[i+1] == 'N':
			out = append(out, "|NULL"...)
			i++
		case enc[i] == 0 && i+1 < len(enc) && enc[i+1] == 'V':
			out = append(out, '|')
			i++
		default:
			out = append(out, enc[i])
		}
	}
	return string(out)
}
