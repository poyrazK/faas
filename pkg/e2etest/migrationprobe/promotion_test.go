package migrationprobe

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	probesql "github.com/onebox-faas/faas/pkg/e2etest/migrationprobe/sqlc"
)

func TestPromotionCommittedEffectAndDuplicateInvocation(t *testing.T) {
	for _, mode := range []string{"success", "failure", "hold"} {
		t.Run(mode, func(t *testing.T) {
			pool := pgtest.OpenDatabase(t)
			conn, err := pool.Acquire(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Release()
			c := Config{RunID: uuid.NewString(), Major: 16}
			if c.initialize(t.Context(), conn.Conn()) != nil || c.initializePromotion(t.Context(), conn.Conn(), mode, mode == "success") != nil {
				t.Fatal("initialize isolated promotion SQL")
			}
			q := probesql.New()
			id, _ := c.ID()
			if mode == "hold" {
				ctx, cancel := context.WithCancel(t.Context())
				done := make(chan error, 1)
				go func() { done <- c.promote(ctx, conn.Conn()) }()
				defer cancel()
				deadline := time.Now().Add(5 * time.Second)
				committed := false
				for time.Now().Before(deadline) {
					row, readErr := q.ObservePromotion(t.Context(), pool, id)
					if readErr != nil {
						t.Fatal("observe committed expansion")
					}
					if row.Applied && row.Executions == 1 {
						committed = true
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				cancel()
				select {
				case err := <-done:
					if !errors.Is(err, ErrProbe) || !committed {
						t.Fatal("lost result did not preserve a committed SQL effect")
					}
				case <-time.After(5 * time.Second):
					t.Fatal("held result did not stop")
				}
			} else {
				err := c.promote(t.Context(), conn.Conn())
				if mode == "success" && err != nil || mode == "failure" && !errors.Is(err, ErrPromotionCommand) {
					t.Fatal("wrong command outcome")
				}
			}
			row, err := q.ReadCurrent(t.Context(), pool, id)
			if err != nil || row.Value != c.Marker() {
				t.Fatal("lost/nonzero result reversed schema expansion or customer data")
			}
			// Replays increment the invocation counter even though DDL would fail.
			// A unique SQL marker alone would falsely hide this second execution.
			replay, err := pool.Acquire(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer replay.Release()
			if !errors.Is(c.promote(t.Context(), replay.Conn()), ErrProbe) {
				t.Fatal("duplicate command was accepted")
			}
			state, err := q.ObservePromotion(t.Context(), pool, id)
			if err != nil || state.Executions != 2 || !state.Applied {
				t.Fatal("duplicate invocation disappeared from customer SQL evidence")
			}
		})
	}
}
