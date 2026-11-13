// adr: 20261112193306
package commitmanaged

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// A row's changes reach the queue in commit order under the built-in ordered
// policy, and a held change holds only its own row.
func TestChangesSourceOrdersEachRowsChanges(t *testing.T) {
	e := newChangesEnv(t, true)
	ctx := t.Context()
	for _, stmt := range []string{
		`INSERT INTO public.orders VALUES (1, 'new', NULL)`,
		`INSERT INTO public.orders VALUES (2, 'new', NULL)`,
		`UPDATE public.orders SET status = 'paid' WHERE id = 1`,
		`UPDATE public.orders SET status = 'shipped' WHERE id = 1`,
		`UPDATE public.orders SET status = 'paid' WHERE id = 2`,
	} {
		if _, err := e.customer.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	var keys []string
	rows, err := e.customer.Query(ctx, `SELECT DISTINCT group_key FROM public.gregale_outbox ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, k)
	}
	rows.Close()
	if want := []string{`orders:{"id": 1}`, `orders:{"id": 2}`}; !slices.Equal(keys, want) {
		t.Fatalf("group keys %q, want %q", keys, want)
	}

	// Hold row 1's first change, as a permanently refused change would be.
	if _, err := e.customer.Exec(ctx, `UPDATE public.gregale_outbox SET blocked_code = 'held'
 WHERE event_id = (SELECT event_id FROM public.gregale_outbox WHERE group_key = 'orders:{"id": 1}' ORDER BY created_at, event_id LIMIT 1)`); err != nil {
		t.Fatal(err)
	}
	if err := e.manager.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if got := e.delivered(t); !slices.Equal(got, []string{"2:new", "2:paid"}) {
		t.Fatalf("with row 1 held, delivered %v; want only row 2, in order", got)
	}

	// Replay releases the held change; row 1 then drains in order in one tick.
	if _, err := e.customer.Exec(ctx, `UPDATE public.gregale_outbox SET blocked_code = NULL, next_attempt_at = clock_timestamp()`); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(2 * time.Minute)
	if err := e.manager.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.pending(t); n != 0 {
		t.Fatalf("%d changes left after the held change was released", n)
	}
	if got := e.delivered(t); !slices.Equal(got, []string{"2:new", "2:paid", "1:new", "1:paid", "1:shipped"}) {
		t.Fatalf("delivered %v", got)
	}

	// Every change is keyed work of the built-in policy, one lane per row.
	var policies, lanes int
	if err := e.control.QueryRow(ctx, `SELECT count(DISTINCT work_policy_name), count(DISTINCT work_key_digest) FROM invocations
 WHERE app_id = $1::uuid AND work_policy_name = $2`, e.app.ID, workpolicy.Ordered).Scan(&policies, &lanes); err != nil {
		t.Fatal(err)
	}
	if policies != 1 || lanes != 2 {
		t.Fatalf("ordered work: %d policies, %d lanes; want 1 and 2", policies, lanes)
	}
}

// delivered lists the queued changes as "<id>:<status>" in admission order.
func (e *changesEnv) delivered(t *testing.T) []string {
	t.Helper()
	rows, err := e.control.Query(t.Context(), `SELECT payload FROM invocations WHERE app_id = $1::uuid
 ORDER BY created_at, id`, e.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			t.Fatal(err)
		}
		var env struct {
			Data struct {
				After struct {
					ID     json.Number `json:"id"`
					Status string      `json:"status"`
				} `json:"after"`
			} `json:"data"`
		}
		if err := json.Unmarshal(payload, &env); err != nil {
			t.Fatal(err)
		}
		out = append(out, env.Data.After.ID.String()+":"+env.Data.After.Status)
	}
	return out
}
