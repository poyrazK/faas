package main_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func devPostgresSession(state string) api.DevSessionResponse {
	return api.DevSessionResponse{Postgres: &api.DevPostgresResponse{Name: "dev-db", BindingState: state}}
}

// The first sync waits for the developer database's bindings: a release
// command that ran before MIGRATION_DATABASE_URL existed left a new
// environment with nothing to serve.
func TestWaitForDevPostgres(t *testing.T) {
	for _, tc := range []struct {
		name      string
		start     string
		reads     []string
		readErr   error
		want      string
		wantErr   string
		wantReads int
	}{
		{name: "already ready", start: "ready", want: "ready"},
		{name: "waits until ready", start: "provisioning", reads: []string{"provisioning", "ready"}, want: "ready", wantReads: 2},
		{name: "failed binding stops", start: "provisioning", reads: []string{"failed"}, want: "failed", wantErr: "binding is failed", wantReads: 1},
		{name: "read errors keep waiting until the deadline", start: "provisioning", readErr: errors.New("boom"), want: "provisioning"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			get := func(context.Context) (api.DevSessionResponse, error) {
				reads++
				if tc.readErr != nil {
					return api.DevSessionResponse{}, tc.readErr
				}
				return devPostgresSession(tc.reads[min(reads, len(tc.reads))-1]), nil
			}
			got, err := main.WaitForDevPostgres(t.Context(), devPostgresSession(tc.start), 50*time.Millisecond, time.Millisecond, get, nil)
			if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
			if got.Postgres.BindingState != tc.want {
				t.Fatalf("state = %q, want %q", got.Postgres.BindingState, tc.want)
			}
			if tc.wantReads != 0 && reads != tc.wantReads {
				t.Fatalf("reads = %d, want %d", reads, tc.wantReads)
			}
		})
	}
}

func TestWaitForDevPostgresWithoutDatabaseNeverReads(t *testing.T) {
	_, err := main.WaitForDevPostgres(t.Context(), api.DevSessionResponse{}, time.Second, time.Millisecond, func(context.Context) (api.DevSessionResponse, error) {
		t.Fatal("read a session without a database")
		return api.DevSessionResponse{}, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
}
