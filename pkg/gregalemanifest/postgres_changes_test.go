// adr: 20261112140000
package gregalemanifest

import (
	"strings"
	"testing"
)

func TestPostgresChangesManifest(t *testing.T) {
	base := `schema_version: 1
worker:
  scale: { min: 0, max: 1, metric: queue_depth, target: 100 }
queue_bindings:
  - name: order-changes
    queue_name: order-changes
`
	for _, tc := range []struct {
		name, block, wantErr string
	}{
		{"managed", `postgres_changes:
  database: shop
  queue_name: order-changes
  poll_interval: 5m
  feeds:
    - name: orders
      table: public.orders
      columns: [id, status]
      operations: [insert, update]
`, ""},
		{"byo", `postgres_changes:
  connection_url_env: ORDERS_DATABASE_URL
  queue_name: order-changes
  feeds: [{name: orders, table: orders, columns: [id]}]
`, ""},
		{"both", `postgres_changes:
  database: shop
  connection_url_env: X
  queue_name: order-changes
  feeds: [{name: orders, table: orders, columns: [id]}]
`, "exactly one"},
		{"neither", `postgres_changes:
  queue_name: order-changes
  feeds: [{name: orders, table: orders, columns: [id]}]
`, "exactly one"},
		{"unknown queue", `postgres_changes:
  database: shop
  queue_name: nope
  feeds: [{name: orders, table: orders, columns: [id]}]
`, "queue_bindings"},
		{"bad interval", `postgres_changes:
  database: shop
  queue_name: order-changes
  poll_interval: 500ms
  feeds: [{name: orders, table: orders, columns: [id]}]
`, "poll_interval"},
		{"long interval", `postgres_changes:
  database: shop
  queue_name: order-changes
  poll_interval: 11m
  feeds: [{name: orders, table: orders, columns: [id]}]
`, "poll_interval"},
		{"no feeds", `postgres_changes:
  database: shop
  queue_name: order-changes
`, "feeds"},
		{"bad op", `postgres_changes:
  database: shop
  queue_name: order-changes
  feeds: [{name: orders, table: orders, columns: [id], operations: [truncate]}]
`, "operation"},
		{"typo key", `postgres_changes:
  database: shop
  queue: order-changes
  feeds: [{name: orders, table: orders, columns: [id]}]
`, "queue"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, err := ParseBytes([]byte(base + tc.block))
			if err == nil {
				err = m.Validate()
			}
			if tc.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				req, rerr := m.PostgresChanges.Request()
				if rerr != nil || req.QueueName != "order-changes" {
					t.Fatalf("request %+v (%v)", req, rerr)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}
