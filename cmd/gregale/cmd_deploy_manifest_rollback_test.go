package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdDeploy_RollsBackManifestTriggersWhenDeploymentRejected(t *testing.T) {
	var events []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps":
			events = append(events, "create-app")
			writeJSONTest(w, api.AppResponse{ID: "app-1", Slug: "rollback-app"})
		case "/v1/crons":
			switch r.Method {
			case http.MethodGet:
				events = append(events, "list-crons")
				writeJSONTest(w, []api.CronResponse{})
			case http.MethodPost:
				events = append(events, "create-cron")
				writeJSONTest(w, api.CronResponse{ID: "cron-new", AppID: "rollback-app", Schedule: "0 3 * * *", Path: "/run", Enabled: true})
			default:
				http.Error(w, "unexpected cron method", http.StatusMethodNotAllowed)
			}
		case "/v1/account":
			events = append(events, "whoami")
			writeJSONTest(w, api.AccountResponse{Plan: "pro"})
		case "/v1/apps/rollback-app/deployments":
			events = append(events, "deploy")
			p := &api.Problem{Status: http.StatusUnprocessableEntity, Code: "synthetic_rejection", Title: "Invalid source", Detail: "synthetic deployment rejection"}
			api.WriteProblem(w, p)
		case "/v1/crons/cron-new":
			if r.Method != http.MethodDelete {
				http.Error(w, "unexpected trigger method", http.StatusMethodNotAllowed)
				return
			}
			events = append(events, "delete-cron")
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected route", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	t.Chdir(t.TempDir())
	if err := os.WriteFile("gregale.yaml", []byte(`triggers:
  - kind: cron
    app: rollback-app
    schedule: "0 3 * * *"
    path: /run
`), 0o644); err != nil {
		t.Fatal(err)
	}
	tarball := filepath.Join(t.TempDir(), "source.tar.gz")
	if err := os.WriteFile(tarball, []byte("not-a-real-tarball"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldJSON := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = oldJSON }()
	oldOut, oldErr := osStdout, osStderr
	var stdout, stderr bytes.Buffer
	osStdout, osStderr = &stdout, &stderr
	defer func() { osStdout, osStderr = oldOut, oldErr }()

	if code := cmdDeployTarball([]string{"--tarball", tarball, "--dockerfile", "--name", "rollback-app", "--no-wait"}); code == 0 {
		t.Fatal("deploy exit = 0, want rejected deployment failure")
	}
	if got, want := events, []string{"create-app", "list-crons", "whoami", "create-cron", "deploy", "delete-cron"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("request sequence = %v, want %v", got, want)
	}
	if !strings.Contains(stderr.String(), "Manifest trigger rollback complete") {
		t.Fatalf("stderr missing rollback confirmation: %s", stderr.String())
	}
}

func TestCmdDeploySourceRef_RollsBackManifestTriggersWhenRejected(t *testing.T) {
	var events []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/crons":
			switch r.Method {
			case http.MethodGet:
				events = append(events, "list-crons")
				writeJSONTest(w, []api.CronResponse{})
			case http.MethodPost:
				events = append(events, "create-cron")
				writeJSONTest(w, api.CronResponse{ID: "cron-source-ref", AppID: "rollback-app", Schedule: "0 3 * * *", Path: "/run", Enabled: true})
			default:
				http.Error(w, "unexpected cron method", http.StatusMethodNotAllowed)
			}
		case "/v1/account":
			events = append(events, "whoami")
			writeJSONTest(w, api.AccountResponse{Plan: "pro"})
		case "/v1/apps/rollback-app/deployments/source-ref":
			events = append(events, "source-ref")
			p := &api.Problem{Status: http.StatusUnprocessableEntity, Code: "synthetic_rejection", Title: "Invalid source", Detail: "synthetic source-ref rejection"}
			api.WriteProblem(w, p)
		case "/v1/crons/cron-source-ref":
			if r.Method != http.MethodDelete {
				http.Error(w, "unexpected trigger method", http.StatusMethodNotAllowed)
				return
			}
			events = append(events, "delete-cron")
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected route", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	t.Chdir(t.TempDir())
	if err := os.WriteFile("gregale.yaml", []byte(`triggers:
  - kind: cron
    app: rollback-app
    schedule: "0 3 * * *"
    path: /run
`), 0o644); err != nil {
		t.Fatal(err)
	}

	oldJSON := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = oldJSON }()
	oldOut, oldErr := osStdout, osStderr
	var stdout, stderr bytes.Buffer
	osStdout, osStderr = &stdout, &stderr
	defer func() { osStdout, osStderr = oldOut, oldErr }()

	if code := cmdDeployTarball([]string{"--repo", "onebox-faas/hello", "--ref", "main", "--name", "rollback-app", "--no-wait"}); code == 0 {
		t.Fatal("source-ref deploy exit = 0, want rejected deployment failure")
	}
	if got, want := events, []string{"list-crons", "whoami", "create-cron", "source-ref", "delete-cron"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("request sequence = %v, want %v", got, want)
	}
	if !strings.Contains(stderr.String(), "Manifest trigger rollback complete") {
		t.Fatalf("stderr missing rollback confirmation: %s", stderr.String())
	}
}
