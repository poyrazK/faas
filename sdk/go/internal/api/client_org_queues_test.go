package api

// adr: 202610171908

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOrgQueueClientOwnershipPaths(t *testing.T) {
	var request string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request = r.Method + " " + r.URL.EscapedPath()
		if r.Header.Get("Authorization") != "Bearer company-token" {
			t.Error("missing company credential")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	c := NewClient(server.URL, "company-token")
	ctx := context.Background()
	for _, tc := range []struct {
		want string
		call func() error
	}{
		{"POST /v1/orgs/my%20company/apps/app%2Fid/queues/send", func() error {
			_, err := c.SendOrgQueueMessage(ctx, "my company", "app/id", QueueSendRequest{QueueName: "jobs"})
			return err
		}},
		{"POST /v1/orgs/my%20company/apps/app%2Fid/queues/jobs%2Fid/leases", func() error {
			_, err := c.LeaseOrgQueueMessages(ctx, "my company", "app/id", "jobs/id", QueueLeaseRequest{})
			return err
		}},
		{"POST /v1/orgs/my%20company/apps/app%2Fid/queues/jobs%2Fid/leases:ack", func() error {
			_, err := c.AckOrgQueueLeases(ctx, "my company", "app/id", "jobs/id", QueueLeaseReceiptsRequest{Receipts: []string{"receipt"}})
			return err
		}},
		{"POST /v1/orgs/my%20company/apps/app%2Fid/queues/dead_letter/message%2Fid/replay", func() error {
			_, err := c.ReplayOrgQueueDeadLetter(ctx, "my company", "app/id", "message/id")
			return err
		}},
		{"GET /v1/orgs/my%20company/projects/project%2Fid/environments/stage/workloads/worker%2Fid/queue-bindings", func() error {
			_, err := c.GetOrgProjectEnvironmentQueueBindings(ctx, "my company", "project/id", "stage", "worker/id")
			return err
		}},
		{"GET /v1/orgs/my%20company/queue-usage", func() error { _, err := c.GetOrgQueueUsage(ctx, "my company"); return err }},
	} {
		if err := tc.call(); err != nil {
			t.Fatal(err)
		}
		if request != tc.want {
			t.Fatalf("request %s; want %s", request, tc.want)
		}
	}
}
