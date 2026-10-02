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
	"sync/atomic"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestVerifyGithubActionRegion(t *testing.T) {
	sha := strings.Repeat("a", 40)
	for _, tc := range []struct {
		name, manifest                           string
		manifestStatus, bundleStatus, bundleSize int
		wantErr                                  bool
	}{
		{name: "released", manifest: `{"schema_version":1,"compute_region":true}`, bundleSize: 100},
		{name: "old", manifestStatus: 404, wantErr: true},
		{name: "disabled", manifest: `{"schema_version":1,"compute_region":false}`, wantErr: true},
		{name: "new schema", manifest: `{"schema_version":2,"compute_region":true}`, wantErr: true},
		{name: "bad json", manifest: `{`, wantErr: true},
		{name: "oversized", manifest: strings.Repeat("x", 4097), wantErr: true},
		{name: "unreleased source", manifest: `{"schema_version":1,"compute_region":true}`, bundleStatus: 404, wantErr: true},
		{name: "empty bundle", manifest: `{"schema_version":1,"compute_region":true}`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				root := "/" + githubActionRepo + "/" + sha + "/" + githubActionPath
				switch {
				case r.Method == http.MethodGet && r.URL.Path == root+"/src/capabilities.json":
					status := tc.manifestStatus
					if status == 0 {
						status = 200
					}
					w.WriteHeader(status)
					_, _ = fmt.Fprint(w, tc.manifest)
				case r.Method == http.MethodHead && r.URL.Path == root+"/bin/gregale":
					status := tc.bundleStatus
					if status == 0 {
						status = 200
					}
					w.Header().Set("Content-Length", fmt.Sprint(tc.bundleSize))
					w.WriteHeader(status)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer srv.Close()
			err := verifyGithubActionRegion(t.Context(), srv.Client(), srv.URL, strings.ToUpper(sha))
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr=%t", err, tc.wantErr)
			}
		})
	}
}

func TestVerifyGithubActionRegionRejectsBadPinAndCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s", r.URL)
	}))
	defer srv.Close()
	if err := verifyGithubActionRegion(t.Context(), srv.Client(), srv.URL, "v0"); err == nil {
		t.Fatal("accepted a mutable pin")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := verifyGithubActionRegion(ctx, srv.Client(), srv.URL, strings.Repeat("a", 40)); err == nil {
		t.Fatal("ignored cancelled context")
	}
}

func TestGithubRegionSnippetEmbeddedCapability(t *testing.T) {
	oldSupport := githubActionRegionSupportSHA
	t.Cleanup(func() { githubActionRegionSupportSHA = oldSupport })
	for _, tc := range []struct {
		name, support, region string
		wantCode              int
	}{
		{"matching capability", githubActionDefaultSHA, "eu-fsn", 0},
		{"numeric identifier", githubActionDefaultSHA, "001", 0},
		{"boolean identifier", githubActionDefaultSHA, "true", 0},
		{"null identifier", githubActionDefaultSHA, "null", 0},
		{"unverified default", "", "eu-fsn", 1},
		{"stale capability", strings.Repeat("f", 40), "eu-fsn", 1},
		{"explicit empty", githubActionDefaultSHA, "", 1},
		{"invalid region", githubActionDefaultSHA, "EU-fsn", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			githubActionRegionSupportSHA = tc.support
			var stdout, stderr bytes.Buffer
			oldOut, oldErr := osStdout, osStderr
			osStdout, osStderr = &stdout, &stderr
			defer func() { osStdout, osStderr = oldOut, oldErr }()
			code := cmdDeployTarball([]string{"--github", "--name", "regional-app", "--region", tc.region})
			if code != tc.wantCode {
				t.Fatalf("code=%d stderr=%s", code, &stderr)
			}
			if tc.wantCode != 0 {
				if stdout.Len() != 0 {
					t.Fatalf("failure emitted workflow: %s", &stdout)
				}
				return
			}
			var workflow struct {
				Jobs map[string]struct {
					Steps []struct {
						With map[string]yaml.Node `yaml:"with"`
					} `yaml:"steps"`
				} `yaml:"jobs"`
			}
			if err := yaml.Unmarshal(stdout.Bytes(), &workflow); err != nil {
				t.Fatal(err)
			}
			if got := workflow.Jobs["deploy"].Steps[0].With["region"]; got.Value != tc.region || got.Tag != "!!str" {
				t.Fatalf("region=%q tag=%q", got.Value, got.Tag)
			}
		})
	}
}

func TestGithubDeployActionRegionMetadata(t *testing.T) {
	root, err := findRepoRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, ".github/actions/deploy/action.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		Inputs map[string]struct {
			Default  string `yaml:"default"`
			Required bool   `yaml:"required"`
		} `yaml:"inputs"`
		Runs struct {
			Steps []struct {
				Name string            `yaml:"name"`
				Env  map[string]string `yaml:"env"`
			} `yaml:"steps"`
		} `yaml:"runs"`
	}
	if err := yaml.Unmarshal(raw, &metadata); err != nil {
		t.Fatal(err)
	}
	input, ok := metadata.Inputs["region"]
	if !ok || input.Default != "" || input.Required {
		t.Fatalf("region input=%+v present=%t", input, ok)
	}
	for _, name := range []string{"Validate inputs", "Deploy"} {
		found := false
		for _, step := range metadata.Runs.Steps {
			if step.Name == name {
				found = true
				if step.Env["INPUT_REGION"] != "${{ inputs.region }}" {
					t.Errorf("%s region env=%q", name, step.Env["INPUT_REGION"])
				}
			}
		}
		if !found {
			t.Errorf("missing step %q", name)
		}
	}
	manifest, err := os.ReadFile(filepath.Join(root, ".github/actions/deploy/src/capabilities.json"))
	if err != nil {
		t.Fatal(err)
	}
	var capabilities struct {
		SchemaVersion int  `json:"schema_version"`
		ComputeRegion bool `json:"compute_region"`
	}
	if err := json.Unmarshal(manifest, &capabilities); err != nil {
		t.Fatal(err)
	}
	if capabilities.SchemaVersion != 1 || !capabilities.ComputeRegion {
		t.Fatalf("capabilities=%+v", capabilities)
	}
}

type regionActionTransport func(*http.Request) (*http.Response, error)

func (f regionActionTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGithubRegionSnippetExplicitPinVerification(t *testing.T) {
	sha := strings.Repeat("c", 40)
	for _, supported := range []bool{false, true} {
		t.Run(fmt.Sprint(supported), func(t *testing.T) {
			var requests atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				root := "/" + githubActionRepo + "/" + sha + "/" + githubActionPath
				switch {
				case r.Method == http.MethodGet && r.URL.Path == root+"/src/capabilities.json":
					if !supported {
						http.NotFound(w, r)
						return
					}
					_, _ = fmt.Fprint(w, `{"schema_version":1,"compute_region":true}`)
				case r.Method == http.MethodHead && r.URL.Path == root+"/bin/gregale":
					w.Header().Set("Content-Length", "100")
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer srv.Close()
			oldClient := http.DefaultClient
			http.DefaultClient = &http.Client{Transport: regionActionTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Scheme != "https" || r.URL.Host != "raw.githubusercontent.com" {
					t.Fatalf("unexpected capability origin: %s", r.URL)
				}
				forward := r.Clone(r.Context())
				forward.URL.Scheme = "http"
				forward.URL.Host = strings.TrimPrefix(srv.URL, "http://")
				return srv.Client().Transport.RoundTrip(forward)
			})}
			defer func() { http.DefaultClient = oldClient }()
			var stdout, stderr bytes.Buffer
			oldOut, oldErr := osStdout, osStderr
			osStdout, osStderr = &stdout, &stderr
			defer func() { osStdout, osStderr = oldOut, oldErr }()
			code := cmdDeployGithubSnippet(t.Context(), []string{"--app", "regional-app", "--region", "eu-fsn", "--pinned-sha", sha})
			if supported {
				if code != 0 || requests.Load() != 2 || !strings.Contains(stdout.String(), `region: "eu-fsn"`) || !strings.Contains(stdout.String(), "@"+sha) {
					t.Fatalf("code=%d requests=%d stdout=%s stderr=%s", code, requests.Load(), &stdout, &stderr)
				}
			} else if code != 1 || requests.Load() != 1 || stdout.Len() != 0 {
				t.Fatalf("unsupported pin: code=%d requests=%d stdout=%s", code, requests.Load(), &stdout)
			}
		})
	}
}
