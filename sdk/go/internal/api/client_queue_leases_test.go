package api

// adr: 20261112200000

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQueueLeaseClientPaths(t *testing.T) {
	var request string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request = r.Method + " " + r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	c := NewClient(server.URL, "token")
	ctx := context.Background()
	receipts := QueueLeaseReceiptsRequest{Receipts: []string{"r"}}
	for _, tc := range []struct {
		want string
		call func() error
	}{
		{"POST /v1/apps/my%20app/queues/jobs/leases", func() error {
			_, err := c.LeaseQueueMessages(ctx, "my app", "jobs", QueueLeaseRequest{MaxMessages: 1})
			return err
		}},
		{"POST /v1/apps/my%20app/queues/jobs/leases:ack", func() error {
			_, err := c.AckQueueLeases(ctx, "my app", "jobs", receipts)
			return err
		}},
		{"POST /v1/apps/my%20app/queues/jobs/leases:nack", func() error {
			_, err := c.NackQueueLeases(ctx, "my app", "jobs", receipts)
			return err
		}},
		{"POST /v1/apps/my%20app/queues/jobs/leases:extend", func() error {
			_, err := c.ExtendQueueLeases(ctx, "my app", "jobs", receipts)
			return err
		}},
	} {
		if err := tc.call(); err != nil {
			t.Fatal(err)
		}
		if request != tc.want {
			t.Fatalf("request %s; want %s", request, tc.want)
		}
	}
}
