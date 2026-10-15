package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestStartDotenvRequiresReviewedSealing(t *testing.T) {
	stdout, stderr := startTestEnvironment(t)
	source := startTestSource(t)
	writeFile(t, source, "needs-env.js", "console.log(process.env.STRIPE_SECRET_KEY);\n")
	writeFile(t, source, ".env", "STRIPE_SECRET_KEY="+fakeStripeLiveKey+"\n")
	runner := startTestRunner(t, source, "3\n")
	report, ready, err := runner.sourcePreflight()
	if err != nil || ready || !report.HasErrors() || runner.secretsFile != "" {
		t.Fatalf("local dotenv was treated as delivered configuration: ready=%t err=%v", ready, err)
	}
	if !strings.Contains(stdout.String(), "STRIPE_SECRET_KEY") || strings.Contains(stdout.String()+stderr(), fakeStripeLiveKey) {
		t.Fatal("missing-key guidance was absent or exposed its value")
	}
	runner.prompt.reader.Reset(strings.NewReader("4\n" + filepath.Join(source, ".env") + "\nn\n"))
	if code, submitted := runner.reviewAndDeploy(); code != 0 || submitted || runner.secretsFile == "" {
		t.Fatalf("reviewing then declining dotenv should not deploy: code=%d submitted=%t stderr=%s stdout=%s", code, submitted, stderr(), stdout)
	}
	if !strings.Contains(stdout.String(), "Seal these keys before deployment: STRIPE_SECRET_KEY") {
		t.Fatal("the confirmation omitted the selected secret key")
	}
	if _, err := os.Stat(runner.statePath); !os.IsNotExist(err) {
		t.Fatal("declined configuration created a session")
	}
}

func TestStartConfigurationReconcilesRuntimeKeyNames(t *testing.T) {
	for _, tc := range []struct {
		name, secretScope, localValue string
		env, secret, declared         bool
		denied, ready                 bool
	}{
		{name: "remote environment", env: true, ready: true},
		{name: "remote default secret", secret: true, secretScope: "default", ready: true},
		{name: "another scope cannot satisfy default", secret: true, secretScope: "staging"},
		{name: "metadata denial remains unverified", denied: true},
		{name: "empty replacement cannot use old value", secret: true, secretScope: "default", localValue: "empty"},
		{name: "required declaration needs a sealed secret", env: true, declared: true},
		{name: "required declaration uses remote sealed secret", secret: true, secretScope: "default", declared: true, ready: true},
		{name: "selected local file", localValue: "selected-private-value", ready: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr := startTestEnvironment(t)
			source := startTestSource(t)
			writeFile(t, source, "needs-env.js", "console.log(process.env.FAAS_TEST_ALPHA);\n")
			writeFile(t, source, ".env", "FAAS_TEST_ALPHA=local-only-value\n")
			if tc.declared {
				writeFile(t, source, ".gregale/required-secrets.json", `["FAAS_TEST_ALPHA"]`)
			}
			mutations := 0
			startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					mutations++
				}
				switch r.URL.Path {
				case "/v1/apps/first-app":
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "first-app"})
				case "/v1/apps/first-app/secrets":
					if r.URL.Query().Get("scope") != "default" {
						t.Error("configuration lookup used linked or unspecified scope")
					}
					if tc.denied {
						api.WriteProblem(w, api.NewProblem(403, "forbidden", "Forbidden", "Metadata unavailable"))
						return
					}
					metadata := api.AppSecretListResponse{}
					if tc.secret {
						metadata.Secrets = []api.AppSecretResponse{{Key: "FAAS_TEST_ALPHA", Scope: tc.secretScope}}
					}
					_ = json.NewEncoder(w).Encode(metadata)
				case "/v1/apps/first-app/env":
					metadata := api.AppEnvListResponse{}
					if tc.env {
						metadata.Env = []api.AppEnvResponse{{Key: "FAAS_TEST_ALPHA"}}
					}
					_ = json.NewEncoder(w).Encode(metadata)
				default:
					http.NotFound(w, r)
				}
			})
			runner := startTestRunner(t, source, "3\n")
			runner.session.AppID = "app-1"
			if tc.localValue != "" {
				value := tc.localValue
				if value == "empty" {
					value = ""
				}
				runner.secretsFile = filepath.Join(t.TempDir(), "selected.env")
				writeFile(t, filepath.Dir(runner.secretsFile), "selected.env", "FAAS_TEST_ALPHA="+value+"\n")
			}
			report, ready, err := runner.sourcePreflight()
			if ready != tc.ready || err != nil || mutations != 0 {
				t.Fatalf("ready=%t want=%t mutations=%d err=%v stdout=%s", ready, tc.ready, mutations, err, stdout)
			}
			if tc.denied {
				unverified := false
				for _, check := range report.Checks {
					unverified = unverified || check.Code == "configuration_unverified"
				}
				if !unverified {
					t.Fatal("unreadable metadata was reported as missing or valid configuration")
				}
			}
			if strings.Contains(stdout.String()+stderr(), "selected-private-value") || strings.Contains(stdout.String()+stderr(), "local-only-value") {
				t.Fatal("configuration validation exposed values")
			}
		})
	}
}

func TestStartSelectedDotenvIsSealedBeforeUpload(t *testing.T) {
	stdout, stderr := startTestEnvironment(t)
	source := startTestSource(t)
	withCwd(t, source)
	writeFile(t, source, "needs-env.js", "console.log(process.env.STRIPE_SECRET_KEY);\n")
	writeFile(t, source, ".env", "STRIPE_SECRET_KEY="+fakeStripeLiveKey+"\n")
	sealed, uploaded := false, false
	startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "first-app"})
		case r.Method == http.MethodPut && r.URL.Path == "/v1/apps/first-app/secrets/STRIPE_SECRET_KEY":
			sealed = true
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/first-app/deployments":
			uploaded = true
			if !sealed {
				t.Error("upload began before sealing the selected dotenv")
			}
			reader, err := r.MultipartReader()
			if err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			part, err := reader.NextPart()
			for err == nil {
				if part.FileName() != "" {
					data, readErr := io.ReadAll(part)
					if readErr != nil {
						t.Error(readErr)
						return
					}
					archive := readCapturedDeployArchive(t, data)
					for name, body := range archive {
						if filepath.Base(name) == ".env" || bytes.Contains(body, []byte(fakeStripeLiveKey)) {
							t.Error("selected dotenv reached the source archive")
						}
					}
				}
				part, err = reader.NextPart()
			}
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", AppID: "app-1", Status: "pending"})
		case r.URL.Path == "/v1/deployments/d1/logs":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: status\ndata: {\"status\":\"failed\"}\n\n"))
		case r.URL.Path == "/v1/deployments/d1":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", AppID: "app-1", Status: "failed"})
		default:
			api.WriteProblem(w, api.NewProblem(404, "not_found", "Not found", "No resource"))
		}
	})
	pipeStdin(t, "\n4\n"+filepath.Join(source, ".env")+"\ny\n3\n")
	_ = cmdStart(nil)
	if !sealed || !uploaded || strings.Contains(stdout.String()+stderr(), fakeStripeLiveKey) {
		t.Fatal("selected dotenv was not sealed and uploaded with values kept out of output")
	}
	statePath, _ := startSessionPath(source)
	state, err := os.ReadFile(statePath)
	if err != nil || bytes.Contains(state, []byte(fakeStripeLiveKey)) || bytes.Contains(state, []byte(`"secrets_file"`)) {
		t.Fatal("secret configuration leaked into recovery state")
	}
}

func TestStartUnverifiedConfigurationCanUseReviewedFile(t *testing.T) {
	stdout, stderr := startTestEnvironment(t)
	source := startTestSource(t)
	writeFile(t, source, "needs-env.js", "console.log(process.env.FAAS_TEST_ALPHA);\n")
	file := filepath.Join(t.TempDir(), "selected.env")
	writeFile(t, filepath.Dir(file), "selected.env", "FAAS_TEST_ALPHA=private-replacement-value\n")
	metadataReads, writes := 0, 0
	startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes++
		}
		if r.URL.Path == "/v1/apps/first-app" {
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "first-app"})
		} else {
			metadataReads++
			api.WriteProblem(w, api.NewProblem(403, "forbidden", "Forbidden", "Metadata unavailable"))
		}
	})
	runner := startTestRunner(t, source, "4\n"+file+"\nn\n")
	runner.session.AppID = "app-1"
	code, submitted := runner.reviewAndDeploy()
	if code != 0 || submitted || metadataReads != 1 || writes != 0 || runner.secretsFile != file {
		t.Fatalf("unverified metadata blocked file recovery: code=%d submitted=%t reads=%d writes=%d stderr=%s stdout=%s", code, submitted, metadataReads, writes, stderr(), stdout)
	}
	if !strings.Contains(stdout.String(), "configuration_unverified") || !strings.Contains(stdout.String(), "Seal these keys") || strings.Contains(stdout.String()+stderr(), "private-replacement-value") {
		t.Fatal("configuration recovery failed to show a key-only review")
	}
}
