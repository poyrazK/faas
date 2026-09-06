package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCmdDeploysRetry_LocalValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
		help bool
	}{
		{"help", []string{"--help"}, 0, true},
		{"short help", []string{"-h"}, 0, true},
		{"help after id", []string{retryTestID, "--help"}, 0, true},
		{"help before id", []string{"--help", retryTestID}, 0, true},
		{"help with stage", []string{retryTestID, "--from=image_build", "-h"}, 0, true},
		{"missing id", nil, 1, false},
		{"invalid id", []string{"not-a-uuid"}, 1, false},
		{"invalid id explicit stage", []string{"not-a-uuid", "--from=image_build"}, 1, false},
		{"empty id", []string{""}, 1, false},
		{"path in id", []string{retryTestID + "/stages"}, 1, false},
		{"unknown flag", []string{retryTestID, "--unknown"}, 1, false},
		{"invalid stage", []string{retryTestID, "--from=not_a_stage"}, 1, false},
	}
	for _, entry := range []struct {
		name string
		run  func([]string) int
	}{
		{"handler", cmdDeploysRetry},
		{"process dispatch", func(args []string) int { return run(append([]string{"deploys", "retry"}, args...)) }},
	} {
		t.Run(entry.name, func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					var calls atomic.Int32
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						calls.Add(1)
						w.WriteHeader(http.StatusBadRequest)
					}))
					defer srv.Close()
					t.Setenv("FAAS_API", srv.URL)
					t.Setenv("FAAS_TOKEN", "fp_live_test")
					var out bytes.Buffer
					previous := osStdout
					osStdout = &out
					t.Cleanup(func() { osStdout = previous })
					if got := entry.run(tt.args); got != tt.want {
						t.Errorf("exit = %d, want %d", got, tt.want)
					}
					if got := calls.Load(); got != 0 {
						t.Errorf("API requests = %d, want zero", got)
					}
					if tt.help && !strings.Contains(out.String(), "gregale deploys retry <id> [--from=<stage>]") {
						t.Errorf("missing retry usage: %q", out.String())
					}
				})
			}
		})
	}
}

func TestCmdDeploysRetry_UUIDFormats(t *testing.T) {
	for _, id := range []string{retryTestID, "01234567-89ab-cdef-0123-456789abcdef"} {
		t.Run(id, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/v1/deployments/"+id+"/retry" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write(stubDeploymentResponse(retryTestNewID))
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_test")
			if got := cmdDeploysRetry([]string{id, "--from=image_build"}); got != 0 {
				t.Errorf("exit = %d, want zero", got)
			}
			if got := calls.Load(); got != 1 {
				t.Errorf("API requests = %d, want one", got)
			}
		})
	}
}
