package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func TestMCPControlPlaneCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{name: "supported", body: `{"conditional_parking":true}`, want: "passed"},
		{name: "older report", body: `{"registry_version":1,"plan":"pro","capabilities":[]}`, want: "failed"},
		{name: "disabled backend", body: `{"conditional_parking":false}`, want: "failed"},
		{name: "older endpoint", status: http.StatusNotFound, want: "unknown"},
		{name: "malformed", body: `{"conditional_parking":"yes"}`, want: "unknown"},
		{name: "unavailable", status: http.StatusServiceUnavailable, want: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/capabilities" || r.Header.Get("Authorization") != "Bearer operator" {
					t.Errorf("unexpected compatibility request: %s %s", r.Method, r.URL.Path)
				}
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			check := checkMCPConditionalParking(t.Context(), NewClient(srv.URL, "operator"))
			if check.Name != "conditional_parking" || check.Status != tc.want {
				t.Fatalf("check=%+v", check)
			}
			if tc.want != "passed" && !strings.Contains(strings.ToLower(check.Detail), "upgrade") {
				t.Fatalf("missing upgrade guidance: %+v", check)
			}
			oldOut, oldJSON := osStdout, jsonOutput
			var output bytes.Buffer
			osStdout, jsonOutput = &output, true
			defer func() { osStdout, jsonOutput = oldOut, oldJSON }()
			printMCPHostingDoctor(mcpTasksStatusResult{ControlPlaneChecks: []mcphosting.Check{check}}, "")
			var report struct {
				Checks []mcphosting.Check `json:"checks"`
			}
			if err := json.Unmarshal(output.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if len(report.Checks) == 0 || report.Checks[0] != check {
				t.Fatalf("doctor dropped compatibility: %s", output.String())
			}
		})
	}
}

func TestMCPControlPlaneRejectsAdaptersBeforeMutation(t *testing.T) {
	for _, action := range []string{"start", "drain", "retirement check"} {
		t.Run(action, func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != http.MethodGet || r.URL.Path != "/v1/capabilities" {
					t.Errorf("preflight allowed %s %s", r.Method, r.URL.Path)
				}
				fmt.Fprint(w, `{"registry_version":1,"capabilities":[]}`)
			}))
			defer srv.Close()
			c := NewClient(srv.URL, "operator")
			p := mcpNativeReleasePlan{}
			s := mcpNativeReleaseState{Stage: "web_restored"}
			before := s.Stage
			var err error
			switch action {
			case "start":
				err = startMCPNativeRelease(context.Background(), c, p, &s, filepath.Join(t.TempDir(), "state.json"))
			case "drain":
				err = drainMCPNativeRelease(context.Background(), c, p, &s, filepath.Join(t.TempDir(), "state.json"))
			default:
				err = checkMCPNativeRetirement(context.Background(), c, p, &s)
			}
			if err == nil || !strings.Contains(err.Error(), "Upgrade") || requests != 1 || s.Stage != before {
				t.Fatalf("requests=%d stage=%s err=%v", requests, s.Stage, err)
			}
		})
	}
}

func TestMCPControlPlaneRejectsGatesBeforeSideEffects(t *testing.T) {
	for _, action := range []string{"release", "quarantine", "retire"} {
		t.Run(action, func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != http.MethodGet || r.URL.Path != "/v1/capabilities" {
					t.Errorf("preflight allowed %s %s", r.Method, r.URL.Path)
				}
				fmt.Fprint(w, `{"registry_version":1,"capabilities":[]}`)
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "operator")
			root := t.TempDir()
			for _, file := range []string{"tasks-release.js", "tasks-release-retire.js"} {
				if err := os.WriteFile(filepath.Join(root, file), []byte(`require('fs').writeFileSync('gate-ran', 'yes'); console.log('{"ok":true,"stage":"quarantined"}');`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			state := filepath.Join(root, "journal.json")
			s := mcpNativeReleaseState{Version: 1, Stage: "web_restored", Fingerprint: "fixture", WorkerIDs: []string{"captured-worker"}, PreviousDeployments: map[string]string{}, Parked: map[string]bool{}}
			if action == "release" {
				s.Stage = "prepared"
			} else if err := saveMCPNativeState(state, &s); err != nil {
				t.Fatal(err)
			}
			p := mcpNativeReleasePlan{WorkerPath: root}
			oldOut := osStdout
			var output bytes.Buffer
			osStdout = &output
			defer func() { osStdout = oldOut }()
			code := 0
			if action == "release" {
				code = runMCPNativeRelease(p, s, state, "plan.json")
			} else {
				code = runMCPNativeRetirement(p, s, state, "plan.json", action == "retire")
			}
			if code == 0 || requests != 1 {
				t.Fatalf("code=%d requests=%d", code, requests)
			}
			if _, err := os.Stat(filepath.Join(root, "gate-ran")); !os.IsNotExist(err) {
				t.Fatalf("Node gate ran: %v", err)
			}
			if action == "release" {
				if _, err := os.Stat(state); !os.IsNotExist(err) {
					t.Fatalf("preflight wrote journal: %v", err)
				}
			} else {
				restored, err := loadMCPNativeState(state, s.Fingerprint)
				if err != nil || restored.Stage != s.Stage {
					t.Fatalf("journal changed: %+v %v", restored, err)
				}
			}
		})
	}
}

func TestMCPControlPlaneHostingDoctorIntegration(t *testing.T) {
	for _, supported := range []bool{false, true} {
		t.Run(fmt.Sprint(supported), func(t *testing.T) {
			capabilityReads := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("doctor attempted mutation: %s", r.Method)
				}
				switch r.URL.Path {
				case "/v1/capabilities":
					capabilityReads++
					if supported {
						fmt.Fprint(w, `{"conditional_parking":true}`)
					} else {
						fmt.Fprint(w, `{"registry_version":1,"capabilities":[]}`)
					}
				case "/v1/apps/worker":
					json.NewEncoder(w).Encode(api.AppResponse{ID: "worker-id", Slug: "worker", WorkloadClass: "worker"})
				case "/v1/apps/worker/custom-metrics":
					json.NewEncoder(w).Encode(api.CustomMetricListResponse{})
				default:
					t.Errorf("unexpected doctor path: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "operator")
			oldOut, oldJSON := osStdout, jsonOutput
			var output bytes.Buffer
			osStdout, jsonOutput = &output, true
			defer func() { osStdout, jsonOutput = oldOut, oldJSON }()
			cmdMCPTasksReport([]string{"--app", "worker"}, true)
			var report struct {
				Checks []mcphosting.Check `json:"checks"`
			}
			if err := json.Unmarshal(output.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			want := "failed"
			if supported {
				want = "passed"
			}
			if capabilityReads != 1 || len(report.Checks) == 0 || report.Checks[0].Name != "conditional_parking" || report.Checks[0].Status != want {
				t.Fatalf("reads=%d doctor=%s", capabilityReads, output.String())
			}
			output.Reset()
			capabilityReads = 0
			if code := cmdMCPTasksReport([]string{"--app", "worker"}, false); code != 0 || capabilityReads != 0 {
				t.Fatalf("status required compatibility: code=%d reads=%d", code, capabilityReads)
			}
		})
	}
}
