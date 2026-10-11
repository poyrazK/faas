package main

import (
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
)

func TestDataAPIRefreshCommandHelper(t *testing.T) {
	if os.Getenv("GREGALE_REFRESH_HELPER") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) != 2 {
		os.Exit(2)
	}
	phase := args[1]
	if os.Getenv("GREGALE_DATA_API_REFRESH_TOKEN") != strings.Repeat("a", 64) {
		os.Exit(3)
	}
	if (phase == "resume" || phase == "authorization") && os.Getenv("GREGALE_DATA_API_REFRESH_FINGERPRINT") != os.Getenv("GREGALE_REFRESH_EXPECTED") {
		os.Exit(4)
	}
	if os.Getenv("GREGALE_REFRESH_FAIL") == phase {
		os.Exit(5)
	}
	if phase == "pause" {
		_ = os.WriteFile("paused", nil, 0600)
	}
	if phase == "resume" {
		_ = os.Remove("paused")
	}
	_ = os.WriteFile(phase+"-ran", nil, 0600)
	os.Exit(0)
}

func TestDataAPICoordinatedSyncFailureAndRecovery(t *testing.T) {
	for _, failure := range []string{"", "migration", "client", "authorization", "resume", "ownership", "drift", "legacy-runtime"} {
		t.Run(failure, func(t *testing.T) {
			resetJSONOut(t)
			path, config := dataAPISyncTestConfig(t)
			root := filepath.Dir(path)
			before := dataAPIContractFixture(t, dataAPIDiffFixture())
			parsed, err := parseDataAPIContract(before)
			if err != nil {
				t.Fatal(err)
			}
			_ = os.WriteFile(filepath.Join(root, "baseline.json"), before, 0600)
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			step := func(phase string) dataAPISyncCommand {
				return dataAPISyncCommand{Directory: root, Command: []string{executable, "-test.run=^TestDataAPIRefreshCommandHelper$", "--", phase}}
			}
			config.Coordination = &dataAPISyncCoordination{Pause: step("pause"), Resume: step("resume"), Authorization: step("authorization"), Baseline: "baseline.json"}
			writeDataAPISyncTestConfig(t, path, config)
			t.Setenv("GREGALE_REFRESH_HELPER", "1")
			t.Setenv("GREGALE_DATA_API_REFRESH_TOKEN", strings.Repeat("a", 64))
			t.Setenv("GREGALE_REFRESH_EXPECTED", parsed.Fingerprint)
			if failure == "migration" {
				t.Setenv("GREGALE_DATA_API_SYNC_MIGRATION_FAIL", "1")
			}
			if failure == "client" {
				t.Setenv("GREGALE_DATA_API_SYNC_CHECK_FAIL", "1")
			}
			if failure == "authorization" || failure == "resume" {
				t.Setenv("GREGALE_REFRESH_FAIL", failure)
			}
			types := strings.ReplaceAll(dataAPISyncTestTypes, strings.Repeat("a", 64), parsed.Fingerprint)
			// The legacy check fixture needs its fixed types; authorization below checks the real fingerprint.
			if failure != "client" {
				config.Check = step("client")
				writeDataAPISyncTestConfig(t, path, config)
			}
			resumed, checks := false, 0
			health := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") == "Bearer owner-test-token" {
					t.Error("owner key sent to app")
				}
				if r.URL.Path == "/healthz" {
					writeJSONTest(w, map[string]bool{"ready": true})
					return
				}
				if r.Header.Get("Authorization") != "Bearer application.test.token" {
					t.Error("application JWT missing")
				}
				_, e := os.Stat(filepath.Join(root, "paused"))
				paused := e == nil
				owns := paused && r.Header.Get("X-Gregale-Refresh-Token") == strings.Repeat("a", 64)
				if failure == "ownership" && paused {
					owns = false
				}
				fp := parsed.Fingerprint
				if failure == "drift" && paused {
					checks++
					if checks > 2 {
						fp = strings.Repeat("b", 64)
					}
				}
				if !paused {
					if _, e := os.Stat(filepath.Join(root, "resume-ran")); e == nil {
						resumed = true
					}
				}
				protocol := 1
				if failure == "legacy-runtime" && paused {
					protocol = 0
				}
				writeJSONTest(w, map[string]any{"version": 1, "ready": true, "fingerprint": fp, "refresh_protocol": protocol, "refresh_paused": paused, "refresh_verified": owns, "refresh_fingerprint": parsed.Fingerprint})
			}))
			defer health.Close()
			transport := http.DefaultTransport
			http.DefaultTransport = health.Client().Transport
			defer func() { http.DefaultTransport = transport }()
			management := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method + " " + r.URL.Path {
				case "GET /v1/apps/notes":
					writeJSONTest(w, api.AppResponse{Slug: "notes", URL: health.URL})
				case "POST /v1/apps/notes/restart":
					if _, e := os.Stat(filepath.Join(root, "paused")); e != nil {
						t.Error("refresh without pause")
					}
					writeJSONTest(w, api.AppRestartResponse{WakeID: "wake"})
				case "GET /v1/apps/notes/runtime-config-restarts/wake":
					writeJSONTest(w, api.RuntimeConfigRestartStatusResponse{WakeID: "wake", Status: "completed"})
				case "POST /v1/apps/notes/tasks":
					var req api.CreateAppTaskRequest
					_ = json.NewDecoder(r.Body).Decode(&req)
					content := types
					if len(req.Command) == 3 {
						content = string(before)
					}
					writeJSONTest(w, api.AppTaskResponse{ID: "task", DeploymentID: "deployment", Status: api.AppTaskStatusSucceeded, StdoutTail: content})
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
				}
			}))
			defer management.Close()
			t.Setenv("FAAS_API", management.URL)
			t.Setenv("FAAS_TOKEN", "owner-test-token")
			_, stderr, restore := swapIO(t)
			defer restore()
			code := run([]string{"--json", "data-api", "sync", "notes", "--config", path})
			if (code == 0) != (failure == "") || resumed != (failure == "") {
				t.Fatalf("exit=%d resumed=%v error=%s", code, resumed, stderr())
			}
			if failure != "" {
				if _, e := os.Stat(filepath.Join(root, "paused")); e != nil {
					t.Fatal("failure released barrier")
				}
				if strings.Contains(stderr(), strings.Repeat("a", 64)) {
					t.Fatal("refresh credential leaked")
				}
			}
			if failure == "authorization" || failure == "migration" {
				t.Setenv("GREGALE_DATA_API_REFRESH_TOKEN", strings.Repeat("b", 64))
				if code := run([]string{"data-api", "sync", "notes", "--config", path, "--resume-only"}); code == 0 {
					t.Fatal("foreign token recovered a pause")
				}
				t.Setenv("GREGALE_DATA_API_REFRESH_TOKEN", strings.Repeat("a", 64))
				t.Setenv("GREGALE_REFRESH_FAIL", "")
				if code := run([]string{"data-api", "sync", "notes", "--config", path, "--resume-only"}); code != 0 {
					t.Fatalf("recovery: %s", stderr())
				}
				if !resumed {
					t.Fatal("recovery never resumed")
				}
			}
		})
	}
}

func TestDataAPIRefreshCompatibilityRefusesUnapprovedBreaks(t *testing.T) {
	before, err := parseDataAPIContract(dataAPIContractFixture(t, dataAPIDiffFixture()))
	if err != nil {
		t.Fatal(err)
	}
	changed := dataAPIDiffFixture()
	changed.Tables = []dataAPITable{}
	after, err := parseDataAPIContract(dataAPIContractFixture(t, changed))
	if err != nil {
		t.Fatal(err)
	}
	if checkDataAPIRefreshCompatibility(&dataAPISyncCoordination{baseline: before}, after) == nil {
		t.Fatal("released breaking schema")
	}
	c := &dataAPISyncCoordination{baseline: before, approval: &dataAPIApproval{Version: 1, BaselineFingerprint: before.Fingerprint, CurrentFingerprint: after.Fingerprint}}
	for _, change := range diffDataAPISnapshots(before.Parsed, after.Parsed) {
		if change.Breaking {
			c.approval.Changes = append(c.approval.Changes, dataAPIApprovedChange{Path: change.Path, Reason: change.Reason, Rationale: "reviewed"})
		}
	}
	if err := checkDataAPIRefreshCompatibility(c, after); err != nil {
		t.Fatal(err)
	}
	c.approval.CurrentFingerprint = fmt.Sprintf("%064d", 0)
	if checkDataAPIRefreshCompatibility(c, after) == nil {
		t.Fatal("accepted stale approval")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if runDataAPISyncCommandEnv(ctx, dataAPISyncCommand{Command: []string{"missing"}}, nil) == nil {
		t.Fatal("cancelled command ran")
	}
}

func TestDataAPICoordinationRejectsUnsafeConfiguration(t *testing.T) {
	for _, scenario := range []string{"missing-token", "missing-authorization", "output-overwrites-baseline"} {
		t.Run(scenario, func(t *testing.T) {
			path, config := dataAPISyncTestConfig(t)
			root := filepath.Dir(path)
			baseline := dataAPIContractFixture(t, dataAPIDiffFixture())
			if err := os.WriteFile(filepath.Join(root, "baseline.json"), baseline, 0600); err != nil {
				t.Fatal(err)
			}
			c := &dataAPISyncCoordination{Pause: config.Migrate, Resume: config.Migrate, Authorization: config.Check, Baseline: "baseline.json"}
			config.Coordination = c
			t.Setenv("GREGALE_DATA_API_REFRESH_TOKEN", strings.Repeat("a", 64))
			switch scenario {
			case "missing-token":
				t.Setenv("GREGALE_DATA_API_REFRESH_TOKEN", "")
			case "missing-authorization":
				c.Authorization = dataAPISyncCommand{}
			case "output-overwrites-baseline":
				config.Output = "baseline.json"
			}
			writeDataAPISyncTestConfig(t, path, config)
			if _, err := loadDataAPISyncConfig(path); err == nil {
				t.Fatal("unsafe coordination config accepted")
			}
			content, err := os.ReadFile(filepath.Join(root, "baseline.json"))
			if err != nil || string(content) != string(baseline) {
				t.Fatal("preflight changed review evidence")
			}
		})
	}
}
