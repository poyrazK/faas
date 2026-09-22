// adr: 133
package mirror

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type stubCall struct {
	query string
	args  []any
}

type stubExecer struct {
	sqlc.DBTX // These exec-only queries must not call Query or QueryRow.
	calls     []stubCall
	hook      func(stubCall) error
}

func (s *stubExecer) Exec(_ context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	call := stubCall{query, args}
	s.calls = append(s.calls, call)
	if s.hook != nil {
		if err := s.hook(call); err != nil {
			return pgconn.CommandTag{}, err
		}
	}
	return pgconn.NewCommandTag("INSERT 0 5"), nil
}

func TestRollupOnceWindow(t *testing.T) {
	start := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	for _, delta := range []time.Duration{-time.Hour, 0, time.Hour} {
		t.Run(delta.String(), func(t *testing.T) {
			db := &stubExecer{}
			end := start.Add(delta)
			rows, err := RollupOnce(t.Context(), db, start, end)
			if delta <= 0 {
				if err == nil || len(db.calls) != 0 {
					t.Fatalf("invalid window: err=%v, calls=%d", err, len(db.calls))
				}
				return
			}
			if err != nil || rows != 5 || len(db.calls) != 1 {
				t.Fatalf("rows=%d, err=%v, calls=%d", rows, err, len(db.calls))
			}
			for i, want := range []time.Time{start, end} {
				if got := db.calls[0].args[i].(pgtype.Timestamptz); !got.Valid || !got.Time.Equal(want) {
					t.Errorf("arg %d = %v, want %v", i, got, want)
				}
			}
		})
	}
}

func TestRollupAndSweepWrapErrors(t *testing.T) {
	want := errors.New("database unavailable")
	db := &stubExecer{hook: func(stubCall) error { return want }}
	if _, err := RollupOnce(t.Context(), db, time.Time{}, time.Now()); !errors.Is(err, want) {
		t.Fatalf("rollup error = %v", err)
	}
	if _, err := SweepOldLedgerRows(t.Context(), db, time.Now()); !errors.Is(err, want) {
		t.Fatalf("sweep error = %v", err)
	}
}

func TestRollupLoopRetriesBacklogBeforeSweeping(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	db := &stubExecer{}
	db.hook = func(call stubCall) error {
		if len(db.calls) == 1 {
			return fmt.Errorf("transient rollup failure")
		}
		if strings.Contains(call.query, "DELETE FROM") {
			cancel()
		}
		return nil
	}
	RollupLoop(ctx, db, time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if len(db.calls) != 3 {
		t.Fatalf("calls = %d, want failed rollup, retry, sweep", len(db.calls))
	}
	for _, call := range db.calls[:2] {
		if !strings.Contains(call.query, "INSERT INTO") || !call.args[0].(pgtype.Timestamptz).Time.IsZero() {
			t.Fatalf("retry must cover entire backlog: %+v", call)
		}
	}
	end := db.calls[1].args[1].(pgtype.Timestamptz).Time
	cutoff := db.calls[2].args[0].(pgtype.Timestamptz).Time
	if !cutoff.Equal(end.Add(-DefaultLedgerRetention)) {
		t.Fatalf("sweep cutoff = %v, end = %v", cutoff, end)
	}
}

func TestRollupLoopSweepFailureAndCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	db := &stubExecer{}
	sweeps := 0
	db.hook = func(call stubCall) error {
		if strings.Contains(call.query, "DELETE FROM") {
			sweeps++
			if sweeps == 1 {
				return errors.New("sweep unavailable")
			}
			cancel()
		}
		return nil
	}
	RollupLoop(ctx, db, time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if sweeps != 2 || len(db.calls) != 4 {
		t.Fatalf("sweeps=%d, calls=%d", sweeps, len(db.calls))
	}
	db.calls = nil
	RollupLoop(ctx, db, 0, nil) // Already cancelled; also exercise defaults.
	if len(db.calls) != 0 {
		t.Fatal("cancelled loop touched database")
	}
}
