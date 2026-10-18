package livequery

import (
	"os"
	"path/filepath"
	"testing"
)

// adr: 20261016151947 — an engine restored from a checkpoint resumes from the slot as
// if it had never stopped: writes made while it was down arrive as ordinary
// changes, transactions the slot delivers again are skipped, and only a lost
// slot or a changed plan makes subscribers start over.
func TestCheckpointResume(t *testing.T) {
	h := newHarness(t, chatSchema)
	h.engine.Checkpointed = true
	queries := map[string]string{
		"inbox": `SELECT c.name, m.id AS message, m.body FROM members mb JOIN channels c ON c.id = mb.channel_id
			JOIN messages m ON m.channel_id = c.id WHERE mb.user_id = $1`,
		"empty":  `SELECT c.id, c.name FROM channels c LEFT JOIN messages m ON m.channel_id = c.id WHERE m.id IS NULL`,
		"counts": `SELECT channel_id, count(*) AS n, sum(author) AS s, min(body) AS first FROM messages GROUP BY channel_id`,
		"feed":   `SELECT id, body FROM messages ORDER BY id DESC LIMIT 2`,
		"member": `SELECT id, name FROM channels WHERE EXISTS (SELECT 1 FROM members WHERE channel_id = channels.id)`,
		"all":    `SELECT * FROM messages`,
	}
	for id, sql := range queries {
		if id == "inbox" {
			h.register(id, sql, int64(7))
		} else {
			h.register(id, sql)
		}
	}
	h.round()
	mustExec(t, h.pool, `INSERT INTO channels VALUES (1, 'general', false), (2, 'random', false), (3, 'quiet', false)`)
	mustExec(t, h.pool, `INSERT INTO members VALUES (1, 7), (2, 7)`)
	mustExec(t, h.pool, `INSERT INTO messages VALUES (10, 1, 7, 'hi'), (11, 2, 8, 'yo')`)
	h.round()

	resets := func(deltas []Delta) int {
		n := 0
		for _, d := range deltas {
			if d.Reset {
				n++
			}
		}
		return n
	}
	// Writes before the checkpoint that no round applied yet, and writes
	// while the engine is down, both arrive as changes after the restore.
	mustExec(t, h.pool, `INSERT INTO messages VALUES (12, 1, 7, 'later')`)
	h.restart(false)
	mustExec(t, h.pool, `UPDATE messages SET body = 'edited' WHERE id = 10`)
	mustExec(t, h.pool, `DELETE FROM members WHERE channel_id = 2`)
	if n := resets(h.round()); n != 0 {
		t.Fatalf("%d subscriptions started over after a clean restore", n)
	}
	// A restore right after a round, twice: the slot delivers the applied
	// transactions again, and they are skipped.
	h.round()
	h.restart(false)
	h.restart(false)
	mustExec(t, h.pool, `INSERT INTO messages VALUES (13, 3, 9, 'third')`)
	if n := resets(h.round()); n != 0 {
		t.Fatalf("%d subscriptions started over after back-to-back restores", n)
	}
	if h.engine.Stats.Skipped == 0 {
		t.Fatal("no re-delivered transaction was skipped")
	}
	// A schema change while the engine is down: SELECT * starts over, the
	// rest are recompiled and diffed.
	mustExec(t, h.pool, `ALTER TABLE messages ADD COLUMN edited boolean NOT NULL DEFAULT false`)
	h.restart(false)
	if got := outcomes(h.round()); got != "all=reset counts=- empty=- feed=- inbox=- member=-" {
		t.Fatalf("after a schema change while down: %s", got)
	}
	// A lost slot: every subscription starts over from a full result.
	mustExec(t, h.pool, `INSERT INTO messages VALUES (14, 1, 7, 'lost')`)
	h.restart(true)
	mustExec(t, h.pool, `INSERT INTO messages VALUES (15, 3, 9, 'after')`)
	if n := resets(h.round()); n != len(queries) {
		t.Fatalf("%d of %d subscriptions started over after losing the slot", n, len(queries))
	}
	h.round()

	// Checkpoint files are written atomically and restore the same state.
	path := filepath.Join(t.TempDir(), "live.checkpoint")
	if err := h.engine.WriteCheckpointFile(path); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path) //nolint:forbidigo // the test's own temporary file
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := RestoreEngine(t.Context(), h.pool, h.capture, f); err != nil {
		t.Fatal(err)
	}
}
