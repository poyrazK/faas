//go:build metal

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestSourceStartPinMetal is ADR-1500 amendment 4's acceptance. A Railpack
// source build whose own start cannot serve the app runs the start that
// `gregale deploy --dry-run` shows and passes readiness; an app whose
// Railpack start works keeps it. The pinned shapes are the coding-agent
// eval's (2026-11-17): render-examples/flask-hello-world (app.py, Flask and
// Gunicorn, no main.py: Railpack starts `main:app`) and render-examples/fastapi
// (main.py that only defines the app, fastapi[all]: Railpack runs
// `python main.py`, which exits).
func TestSourceStartPinMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping source start pin metal")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping source start pin metal")
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Setenv("FAAS_E2E_API_HOSTING_SMOKE", "1")
	h := e2etest.Start(t, pool, e2etest.All)
	key := h.SeedAccount(context.Background(), api.PlanHobby)

	const flaskApp = "from flask import Flask\napp = Flask(__name__)\n\n@app.route(\"/\")\ndef hello_world():\n    return \"Hello, World!\"\n"
	const fastapiApp = "from fastapi import FastAPI\n\napp = FastAPI()\n\n\n@app.get(\"/\")\nasync def root():\n    return {\"message\": \"Hello World\"}\n"
	for _, tc := range []struct {
		name      string
		files     map[string]string
		framework string
		command   string
		pinned    bool
		body      string
	}{
		{
			name:      "flask-app-py",
			files:     map[string]string{"app.py": flaskApp, "requirements.txt": "Flask\nGunicorn\n"},
			framework: "flask", command: "gunicorn app:app --bind 0.0.0.0:$PORT", pinned: true,
			body: "Hello, World!",
		},
		{
			name:      "fastapi-all-main-py",
			files:     map[string]string{"main.py": fastapiApp, "requirements.txt": "fastapi[all]\n"},
			framework: "fastapi", command: "uvicorn main:app --host 0.0.0.0 --port $PORT", pinned: true,
			body: `{"message":"Hello World"}`,
		},
		{
			// Railpack's `uvicorn main:app --host 0.0.0.0 --port ${PORT:-8000}` stays.
			name:      "fastapi-uvicorn-main-py",
			files:     map[string]string{"main.py": fastapiApp, "requirements.txt": "fastapi\nuvicorn\n"},
			framework: "fastapi", command: "uvicorn main:app --host 0.0.0.0 --port $PORT",
			body: `{"message":"Hello World"}`,
		},
		{
			// Railpack's `gunicorn --bind 0.0.0.0:${PORT:-8000} main:app` stays.
			name:      "flask-gunicorn-main-py",
			files:     map[string]string{"main.py": flaskApp, "requirements.txt": "flask\ngunicorn\n"},
			framework: "flask", command: "gunicorn main:app --bind 0.0.0.0:$PORT",
			body: "Hello, World!",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(func() {
				if t.Failed() {
					h.DumpLogs(t)
				}
			})
			source := fstest.MapFS{}
			for name, body := range tc.files {
				source[name] = &fstest.MapFile{Data: []byte(body)}
			}
			preview, err := frameworkprofile.Analyze(source)
			if err != nil {
				t.Fatal(err)
			}
			if preview.Framework != tc.framework || preview.StartCommand != tc.command || preview.StartPinned != tc.pinned {
				t.Fatalf("dry-run profile = %+v, want %s %q pinned %t", preview, tc.framework, tc.command, tc.pinned)
			}

			slug := "pin-" + tc.name
			result := runBuildSubtestWithSourceRoot(t, h, pool, key, slug, buildTarGz(t, tc.files), "", false)
			dep, err := state.NewPgStore(pool).DeploymentByID(context.Background(), result.deploymentID)
			if err != nil {
				t.Fatalf("deployment %s: %v", result.deploymentID, err)
			}
			var stored frameworkprofile.Profile
			if err := json.Unmarshal(dep.InferredProfile, &stored); err != nil {
				t.Fatalf("deployment %s inferred profile: %v", dep.ID, err)
			}
			if stored.StartCommand != preview.StartCommand || stored.StartPinned != preview.StartPinned {
				t.Fatalf("deployed profile start %q pinned %t, want the dry-run's %q pinned %t", stored.StartCommand, stored.StartPinned, preview.StartCommand, preview.StartPinned)
			}
			receipt, err := apihostingreceipt.Decode(dep.APIHostingReceipt)
			if err != nil {
				t.Fatalf("deployment %s hosting receipt: %v", dep.ID, err)
			}
			if receipt.Smoke.Status != apihostingreceipt.SmokeVerified {
				t.Fatalf("hosting smoke = %+v, want verified", receipt.Smoke)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			url := gatewayAppURL(h, slug)
			if err := e2etest.WaitForHTTPReady(ctx, t, h.HTTPClient(), url, 5*time.Second); err != nil {
				t.Fatalf("gateway route for %s: %v", slug, err)
			}
			body, status := doGetWithHost(t, h.HTTPClient(), url, slug+".apps.test.example", 30*time.Second)
			if status != http.StatusOK || strings.TrimSpace(string(body)) != tc.body {
				t.Fatalf("GET / for %s: status=%d body=%q, want 200 %q", slug, status, body, tc.body)
			}
			t.Logf("%s: live with start %q (pinned %t), smoke %s, GET / %d %s", slug, stored.StartCommand, stored.StartPinned, receipt.Smoke.Verification, status, body)

			parkCtx, parkCancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer parkCancel()
			if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/park", nil); status != http.StatusAccepted {
				t.Fatalf("park %s: status=%d", slug, status)
			}
			if _, err := e2etest.WaitForInstanceState(parkCtx, t, pool, mustGetAppID(t, h, key, slug), state.StateParked, 40*time.Second); err != nil {
				t.Fatalf("park %s: %v", slug, err)
			}
		})
	}
}
