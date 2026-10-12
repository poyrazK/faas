package main

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type fakePostgresEgressClient struct {
	updates []api.UpdateAppRequest
	err     error
}

func (f *fakePostgresEgressClient) UpdateApp(_ context.Context, _ string, request api.UpdateAppRequest) (api.AppResponse, error) {
	f.updates = append(f.updates, request)
	return api.AppResponse{}, f.err
}

// TestEnsurePostgresEgressOpensThePort pins H9-P110: on production-us a
// ready managed-Postgres binding left the app unable to connect because
// tenant egress allows only TCP 80/443. Binding commands must merge 5432
// into the app's extra ports, or explain why they cannot.
func TestEnsurePostgresEgressOpensThePort(t *testing.T) {
	cases := []struct {
		name      string
		existing  []int
		err       error
		wantPorts []int
		wantCalls int
		wantOut   string
	}{
		{name: "opens and keeps existing ports", existing: []int{6379}, wantPorts: []int{6379, 5432}, wantCalls: 1,
			wantOut: "Opened outbound TCP 5432 for app api"},
		{name: "already open", existing: []int{5432}, wantCalls: 0},
		{name: "plan without extra ports", err: &APIError{Problem: api.Problem{Status: 403, Code: api.CodePlanEgressPortsNotAllowed}},
			wantPorts: []int{5432}, wantCalls: 1, wantOut: "cannot open outbound TCP 5432 on its plan"},
		{name: "other failure", err: errors.New("boom"), wantPorts: []int{5432}, wantCalls: 1,
			wantOut: "gregale app api egress-ports add 5432"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			previous := osStderr
			osStderr = &out
			t.Cleanup(func() { osStderr = previous })
			client := &fakePostgresEgressClient{err: tc.err}
			ensurePostgresEgress(context.Background(), client, api.AppResponse{Slug: "api", EgressPorts: tc.existing})
			if len(client.updates) != tc.wantCalls {
				t.Fatalf("updates = %d, want %d", len(client.updates), tc.wantCalls)
			}
			if tc.wantCalls > 0 && !slices.Equal(*client.updates[0].EgressPorts, tc.wantPorts) {
				t.Fatalf("ports = %v, want %v", *client.updates[0].EgressPorts, tc.wantPorts)
			}
			if !strings.Contains(out.String(), tc.wantOut) {
				t.Fatalf("stderr = %q, want %q", out.String(), tc.wantOut)
			}
		})
	}
}

func TestDeployManifestPostgresBindingsOpensEgress(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `databases:
  - database: orders
`)
	fake := &fakeManifestPostgresClient{
		app:       api.AppResponse{ID: "app-id", Slug: "api"},
		databases: api.ManagedPostgresDatabaseList{Items: []api.ManagedPostgresDatabase{{ID: "db-id", Name: "orders", State: "ready"}}},
		binding:   api.ManagedPostgresBinding{ID: "binding-id", State: "ready", EnvironmentKey: "DATABASE_URL"},
	}
	previous := osStderr
	osStderr = &bytes.Buffer{}
	t.Cleanup(func() { osStderr = previous })
	if err := deployManifestPostgresBindings(context.Background(), fake, "api", dir); err != nil {
		t.Fatal(err)
	}
	if len(fake.updates) != 1 || !slices.Equal(*fake.updates[0].EgressPorts, []int{5432}) {
		t.Fatalf("egress updates = %+v, want one opening 5432", fake.updates)
	}
}
