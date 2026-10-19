package main

// ADR-1742: archive-bound publication and recovery before production promotion.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issues"
)

const sourceMapsDeploymentID = "11111111-1111-4111-8111-111111111111"

func deploySourceMapsFixture(t *testing.T) (map[string]string, *issues.SourceMapSet) {
	t.Helper()
	bundle := "throw new Error('charge failed');\n"
	digest := sha256.Sum256([]byte(bundle))
	in := api.PublishIssueSourceMapsRequest{
		Directory: "/app/dist",
		Manifest: api.IssueSourceMapManifest{Version: 1, Artifacts: []api.IssueSourceMapArtifact{{
			GeneratedFile: "bundle.js", SHA256: hex.EncodeToString(digest[:]),
			Payload: `{"version":3,"sources":["../src/charge.ts"],"names":["charge"],"mappings":"AAAAA","sourcesContent":["PRIVATE_SOURCE"]}`,
		}}},
	}
	raw, err := json.Marshal(in.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	set, err := issues.CompileSourceMaps(in)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"package.json":   `{"name":"exports","scripts":{"start":"node dist/bundle.js"}}`,
		"dist/bundle.js": bundle, "dist/.gregale-issue-maps.json": string(raw),
	}, set
}

func TestDeployIssueSourceMapsArchiveBinding(t *testing.T) {
	files, expected := deploySourceMapsFixture(t)
	archiveFiles := make(map[string]string)
	for name, body := range files {
		archiveFiles["repository/packages/exports/"+name] = body
	}
	archiveFiles["repository/package.json"] = `{"workspaces":["packages/*"]}`
	archive := writeDeploySourceArchive(t, archiveFiles)
	set, snapshot, cleanup, err := prepareDeployIssueSourceMaps(archive, "packages/exports", "dist/.gregale-issue-maps.json", "/app/dist")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if set.Digest != expected.Digest || strings.Contains(set.Request.Manifest.Artifacts[0].Payload, "PRIVATE_SOURCE") {
		t.Fatalf("prepared the wrong or unsanitized manifest: %+v", set.Request)
	}
	// Later changes to the customer's archive cannot change the verified upload.
	before, err := tarballSHA256(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, []byte("replaced after verification"), 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := tarballSHA256(snapshot)
	if err != nil || before != after {
		t.Fatalf("snapshot changed: before=%s after=%s err=%v", before, after, err)
	}
}

func TestDeployIssueSourceMapsRejectInvalidArchiveEvidence(t *testing.T) {
	for _, scenario := range []string{"changed bundle", "missing bundle", "missing manifest", "empty manifest", "invalid manifest", "traversal", "duplicate bundle", "oversized manifest"} {
		t.Run(scenario, func(t *testing.T) {
			files, _ := deploySourceMapsFixture(t)
			switch scenario {
			case "changed bundle":
				files["dist/bundle.js"] = "another build"
			case "missing bundle":
				delete(files, "dist/bundle.js")
			case "missing manifest":
				delete(files, "dist/.gregale-issue-maps.json")
			case "empty manifest":
				files["dist/.gregale-issue-maps.json"] = `{"version":1,"artifacts":[]}`
			case "invalid manifest":
				files["dist/.gregale-issue-maps.json"] = `{`
			case "traversal":
				files["dist/.gregale-issue-maps.json"] = strings.ReplaceAll(files["dist/.gregale-issue-maps.json"], `"bundle.js"`, `"../../outside.js"`)
			case "duplicate bundle":
				var manifest api.IssueSourceMapManifest
				if err := json.Unmarshal([]byte(files["dist/.gregale-issue-maps.json"]), &manifest); err != nil {
					t.Fatal(err)
				}
				manifest.Artifacts = append(manifest.Artifacts, manifest.Artifacts[0])
				raw, err := json.Marshal(manifest)
				if err != nil {
					t.Fatal(err)
				}
				files["dist/.gregale-issue-maps.json"] = string(raw)
			case "oversized manifest":
				files["dist/.gregale-issue-maps.json"] = strings.Repeat(" ", api.IssueSourceMapsUploadMaxBytes+1)
			}
			_, _, cleanup, err := prepareDeployIssueSourceMaps(writeDeploySourceArchive(t, files), "", "dist/.gregale-issue-maps.json", "/app/dist")
			if cleanup != nil {
				cleanup()
			}
			if err == nil {
				t.Fatal("accepted invalid archive evidence")
			}
		})
	}
}

func TestDeployIssueSourceMapsBundleBounds(t *testing.T) {
	directory := t.TempDir()
	data := []byte("bundle")
	if err := os.WriteFile(filepath.Join(directory, "bundle.js"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	digest := sha256.Sum256(data)
	if _, err := verifyDeployIssueBundle(root, "bundle.js", hex.EncodeToString(digest[:]), 3); err == nil {
		t.Fatal("ignored remaining aggregate byte bound")
	}
	if err := os.WriteFile(filepath.Join(directory, "large.js"), bytes.Repeat([]byte("x"), api.IssueSourceMapBundleMaxBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyDeployIssueBundle(root, "large.js", "", api.IssueSourceMapBundlesMaxBytes); err == nil || !strings.Contains(err.Error(), "byte bound") {
		t.Fatalf("ignored per-bundle byte bound: %v", err)
	}
}

func TestDeployIssueSourceMapsSourceSelection(t *testing.T) {
	repo := initZeroConfigRepo(t)
	files, expected := deploySourceMapsFixture(t)
	for name, body := range files {
		writeFile(t, repo, strings.Replace(name, "dist/", "assets/", 1), body)
	}
	runGitCmdOrSkip(t, repo, "add", ".")
	runGitCmdOrSkip(t, repo, "commit", "-q", "-m", "prebuilt bundle and manifest")
	writeFile(t, repo, "assets/bundle.js", "working tree changed after the build")
	headArchive := filepath.Join(t.TempDir(), "head.tar.gz")
	if err := gitArchiveHEAD(repo, headArchive); err != nil {
		t.Fatal(err)
	}
	packed, _, _, err := packGitArchive(headArchive, defaultZeroConfigSourceCapMB, modeSourceTree, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(packed) }()
	set, _, cleanup, err := prepareDeployIssueSourceMaps(packed, "", "assets/.gregale-issue-maps.json", "/app/dist")
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if set.Digest != expected.Digest {
		t.Fatal("HEAD source maps came from the working tree")
	}
	worktree, _, _, err := autoPackSource(repo, repo, false, defaultZeroConfigSourceCapMB, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(worktree) }()
	_, _, cleanup, err = prepareDeployIssueSourceMaps(worktree, "", "assets/.gregale-issue-maps.json", "/app/dist")
	if cleanup != nil {
		cleanup()
	}
	if err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("worktree accepted the old build manifest: %v", err)
	}
}

func TestDeployIssueSourceMapsPackingExclusions(t *testing.T) {
	directory := t.TempDir()
	files, _ := deploySourceMapsFixture(t)
	for name, body := range files {
		writeFile(t, directory, name, body)
	}
	archive, _, _, err := autoPackSource(directory, directory, false, defaultZeroConfigSourceCapMB, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(archive) }()
	_, _, cleanup, err := prepareDeployIssueSourceMaps(archive, "", "dist/.gregale-issue-maps.json", "/app/dist")
	if cleanup != nil {
		cleanup()
	}
	if err == nil || !strings.Contains(err.Error(), "included in the uploaded source") {
		t.Fatalf("used excluded working-tree output: %v", err)
	}
}

func TestDeployIssueSourceMapsFlagsRejectBeforeRequest(t *testing.T) {
	base := []string{"--issue-source-maps", "dist/.gregale-issue-maps.json", "--issue-source-map-directory", "/app/dist"}
	for _, flags := range [][]string{
		{}, {"--no-traffic", "--repo", "owner/repo", "--ref", "main"},
		{"--no-traffic", "--image", "registry/app@sha256:" + strings.Repeat("a", 64)},
		{"--no-traffic", "--project"}, {"--no-traffic", "--create-only"},
		{"--no-traffic", "--plan"}, {"--no-traffic", "--dry-run"}, {"--no-traffic", "--diff"},
		{"--no-traffic", "--template", "hello-node"}, {"--no-traffic", "--github"},
		{"--traffic-percent", "0", "--safe"}, {"--traffic-percent", "0", "--canary-preset", "none"},
	} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_source_maps_guard")
			if code := cmdDeployTarball(append(append([]string{}, base...), flags...)); code == 0 || requests != 0 {
				t.Fatalf("code=%d requests=%d; expected a local refusal", code, requests)
			}
		})
	}
	for _, flags := range [][]string{
		{"--issue-source-maps", "dist/maps.json"}, {"--issue-source-map-directory", "/app/dist"},
		{"--issue-source-maps", "../maps.json", "--issue-source-map-directory", "/app/dist"},
		{"--issue-source-maps", "/maps.json", "--issue-source-map-directory", "/app/dist"},
		{"--issue-source-maps", "dist/maps.json", "--issue-source-map-directory", "relative"},
		{"--issue-source-maps", "dist/maps.json", "--issue-source-map-directory", "/app/../dist"},
	} {
		if code := cmdDeployTarball(append(flags, "--no-traffic")); code == 0 {
			t.Fatalf("accepted incomplete or unsafe flags: %v", flags)
		}
	}
}

func TestPublishDeployIssueSourceMapsReconciliation(t *testing.T) {
	_, set := deploySourceMapsFixture(t)
	for _, scenario := range []string{"ready", "lost response", "server failure committed", "unconfirmed", "unauthorized", "conflict", "wrong deployment", "wrong digest", "wrong count", "not ready", "retired", "current traffic", "current source", "current root", "current unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			puts, gets := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/deployments/"+sourceMapsDeploymentID {
					// Exercise reconciliation on a fresh connection, rather than
					// net/http's transparent replay of an idle keep-alive request.
					if scenario == "lost response" {
						w.Header().Set("Connection", "close")
					}
					current := api.DeploymentResponse{ID: sourceMapsDeploymentID, SourceSHA256: strings.Repeat("b", 64)}
					switch scenario {
					case "current traffic":
						current.TrafficPercent = 100
					case "current source":
						current.SourceSHA256 = strings.Repeat("c", 64)
					case "current root":
						current.SourceRoot = "another/workload"
					case "current unavailable":
						http.Error(w, "unavailable", http.StatusServiceUnavailable)
						return
					}
					_ = json.NewEncoder(w).Encode(current)
					return
				}
				if r.URL.Path != "/v1/apps/exports/deployments/"+sourceMapsDeploymentID+"/issue-source-maps" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				if r.Method == http.MethodPut {
					puts++
					switch scenario {
					case "lost response":
						connection, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						_ = connection.Close()
						return
					case "server failure committed", "unconfirmed":
						http.Error(w, "unavailable", http.StatusServiceUnavailable)
						return
					case "unauthorized":
						http.Error(w, "unauthorized", http.StatusUnauthorized)
						return
					case "conflict":
						http.Error(w, "conflict", http.StatusConflict)
						return
					}
				} else {
					gets++
				}
				metadata := api.IssueSourceMapsMetadata{DeploymentID: sourceMapsDeploymentID, ManifestSHA256: set.Digest, MapCount: 1, Ready: true}
				switch scenario {
				case "wrong deployment":
					metadata.DeploymentID = "22222222-2222-4222-8222-222222222222"
				case "wrong digest":
					metadata.ManifestSHA256 = strings.Repeat("a", 64)
				case "wrong count":
					metadata.MapCount++
				case "not ready", "unconfirmed":
					metadata.Ready = false
				case "retired":
					retired := time.Now()
					metadata.RetiredAt = &retired
				}
				_ = json.NewEncoder(w).Encode(metadata)
			}))
			defer server.Close()
			sourceSHA256 := strings.Repeat("b", 64)
			metadata, err := publishDeployIssueSourceMaps(context.Background(), NewClient(server.URL, "token"), "exports", api.DeploymentResponse{ID: sourceMapsDeploymentID, SourceSHA256: sourceSHA256}, sourceSHA256, "", set)
			wantSuccess := scenario == "ready" || scenario == "lost response" || scenario == "server failure committed"
			wantPuts := 1
			if strings.HasPrefix(scenario, "current ") {
				wantPuts = 0
			}
			if (err == nil) != wantSuccess || (metadata != nil) != wantSuccess || puts != wantPuts {
				t.Fatalf("metadata=%+v err=%v puts=%d gets=%d", metadata, err, puts, gets)
			}
			if (scenario == "unauthorized" || scenario == "conflict") && gets != 0 {
				t.Fatal("reconciled a definitive rejection")
			}
		})
	}
}

func TestDeployIssueSourceMapsCLI(t *testing.T) {
	for _, scenario := range []string{"wait", "no wait", "human no wait", "explicit zero", "explicit key", "timeout", "publication failed", "bundle changed", "replayed other source", "replayed other root"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("TMPDIR", t.TempDir())
			files, set := deploySourceMapsFixture(t)
			if scenario == "bundle changed" {
				files["dist/bundle.js"] += "another build"
			}
			archive := writeDeploySourceArchive(t, files)
			sourceSHA256, err := tarballSHA256(archive)
			if err != nil {
				t.Fatal(err)
			}
			var calls []string
			publicationRecovered := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.Method+" "+r.URL.Path)
				switch {
				case r.URL.Path == "/v1/apps" && r.Method == http.MethodPost:
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "exports"})
				case r.URL.Path == "/v1/apps/exports/deployments" && r.Method == http.MethodPost:
					if err := r.ParseMultipartForm(8 << 20); err != nil {
						t.Error(err)
					}
					if got := r.FormValue("traffic_percent"); got != "0" {
						t.Errorf("traffic_percent=%q; want zero", got)
					}
					upload, _, err := r.FormFile("source")
					if err != nil {
						t.Error(err)
						return
					}
					defer func() { _ = upload.Close() }()
					uploadDigest := sha256.New()
					if _, err := io.Copy(uploadDigest, upload); err != nil || hex.EncodeToString(uploadDigest.Sum(nil)) != sourceSHA256 {
						t.Errorf("uploaded archive changed after verification: %v", err)
					}
					deployment := api.DeploymentResponse{ID: sourceMapsDeploymentID, AppID: "app-1", Status: "pending", SourceSHA256: sourceSHA256}
					if scenario == "replayed other source" {
						deployment.SourceSHA256 = strings.Repeat("f", 64)
					}
					if scenario == "replayed other root" {
						deployment.SourceRoot = "another/workload"
					}
					_ = json.NewEncoder(w).Encode(deployment)
				case strings.HasSuffix(r.URL.Path, "/issue-source-maps"):
					if scenario == "publication failed" && !publicationRecovered {
						http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
						return
					}
					var in api.PublishIssueSourceMapsRequest
					if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
						t.Error(err)
					}
					compiled, err := issues.CompileSourceMaps(in)
					if err != nil || compiled.Digest != set.Digest {
						t.Errorf("wrong published build: %+v %v", in, err)
					}
					_ = json.NewEncoder(w).Encode(api.IssueSourceMapsMetadata{DeploymentID: sourceMapsDeploymentID, ManifestSHA256: set.Digest, MapCount: 1, Ready: true})
				case r.URL.Path == "/v1/deployments/"+sourceMapsDeploymentID:
					status := statusLive
					if scenario == "timeout" {
						status = "pending"
					}
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: sourceMapsDeploymentID, AppID: "app-1", Status: status, SourceSHA256: sourceSHA256})
				default:
					if r.Method != http.MethodGet {
						t.Errorf("unexpected mutation: %s %s", r.Method, r.URL.Path)
					}
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_maps_cli")
			var stdout bytes.Buffer
			oldOut, oldJSON := osStdout, jsonOutput
			osStdout, jsonOutput = &stdout, scenario != "human no wait"
			defer func() { osStdout, jsonOutput = oldOut, oldJSON }()
			stderr, restore := captureStderr(t)
			defer restore()
			args := []string{"--tarball", archive, "--name", "exports", "--no-traffic", "--no-doctor", "--no-triggers", "--issue-source-maps", "dist/.gregale-issue-maps.json", "--issue-source-map-directory", "/app/dist"}
			if scenario == "no wait" || scenario == "human no wait" {
				args = append(args, "--no-wait")
			}
			if scenario == "timeout" {
				args = append(args, "--timeout", "1")
			}
			if scenario == "explicit key" || strings.HasPrefix(scenario, "replayed ") {
				args = append(args, "--idempotency-key", "same-logical-key")
			}
			if scenario == "explicit zero" {
				args[4] = "--traffic-percent=0"
			}
			code := cmdDeployTarball(args)
			if scenario == "bundle changed" {
				if code == 0 || len(calls) != 0 || !strings.Contains(stderr.String(), "digest mismatch") {
					t.Fatalf("code=%d calls=%v stderr=%s", code, calls, stderr.String())
				}
				return
			}
			if strings.HasPrefix(scenario, "replayed ") {
				var problem struct {
					Recovery *deployRecovery `json:"recovery"`
				}
				if err := json.Unmarshal([]byte(stderr.String()), &problem); err != nil || code == 0 || stdout.Len() != 0 || problem.Recovery == nil || problem.Recovery.Stage != "source_maps_binding" || problem.Recovery.ResumeCommand != "" {
					t.Fatalf("unsafe replay recovery: code=%d stderr=%s err=%v", code, stderr.String(), err)
				}
				if strings.Contains(strings.Join(calls, "\n"), "/issue-source-maps") {
					t.Fatalf("published to a different source: %v", calls)
				}
				return
			}
			if scenario == "publication failed" {
				if code == 0 || stdout.Len() != 0 {
					t.Fatalf("unconfirmed publication succeeded: code=%d stdout=%s", code, stdout.String())
				}
				var problem struct {
					Recovery *deployRecovery `json:"recovery"`
				}
				if err := json.Unmarshal([]byte(stderr.String()), &problem); err != nil || problem.Recovery == nil || problem.Recovery.DeploymentID != sourceMapsDeploymentID || problem.Recovery.Stage != "source_maps" || !strings.Contains(problem.Recovery.ResumeCommand, "issues source-maps publish") {
					t.Fatalf("missing bound publication recovery: %s (%v)", stderr.String(), err)
				}
				snapshots, err := filepath.Glob(filepath.Join(os.TempDir(), "gregale-source-maps-recovery-*", "manifest.json"))
				if err != nil || len(snapshots) != 1 || !strings.Contains(problem.Recovery.ResumeCommand, snapshots[0]) {
					t.Fatalf("missing exact-build recovery snapshot: %v (%v)", snapshots, err)
				}
				publicationRecovered = true
				if code := cmdIssues([]string{"source-maps", "publish", "--app", "exports", "--deployment", sourceMapsDeploymentID, "--directory", "/app/dist", "--manifest-file", snapshots[0]}); code != 0 {
					t.Fatalf("publication recovery failed: %d", code)
				}
				deployments := 0
				for _, call := range calls {
					if call == "POST /v1/apps/exports/deployments" {
						deployments++
					}
				}
				if deployments != 1 {
					t.Fatalf("recovery resubmitted the deployment: %v", calls)
				}
				return
			}
			if scenario == "human no wait" {
				if code != 0 || !strings.Contains(stdout.String(), "Source maps ready:") || strings.Contains(stdout.String(), "PRIVATE_SOURCE") {
					t.Fatalf("human output: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
				}
				return
			}
			var receipt DeployReceipt
			wantCode := 0
			if scenario == "timeout" {
				wantCode = 3
			}
			if err := json.Unmarshal(stdout.Bytes(), &receipt); err != nil || code != wantCode || receipt.IssueSourceMaps == nil || !receipt.IssueSourceMaps.Ready || receipt.ID != sourceMapsDeploymentID {
				t.Fatalf("code=%d receipt=%s stderr=%s err=%v", code, stdout.String(), stderr.String(), err)
			}
			if scenario == "timeout" && (!receipt.TimedOut || receipt.Recovery == nil || receipt.Recovery.DeploymentID != sourceMapsDeploymentID) {
				t.Fatalf("timeout lost publication/deployment evidence: %+v", receipt)
			}
			if !strings.Contains(strings.Join(calls, "\n"), "PUT /v1/apps/exports/deployments/"+sourceMapsDeploymentID+"/issue-source-maps") {
				t.Fatalf("publication omitted: %v", calls)
			}
		})
	}
}

func TestDeployIssueSourceMapsRecoverySnapshot(t *testing.T) {
	_, set := deploySourceMapsFixture(t)
	file, err := saveDeployIssueSourceMapsRecovery(set)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(filepath.Dir(file)) }()
	data, err := os.ReadFile(file)
	if err != nil || strings.Contains(string(data), "PRIVATE_SOURCE") {
		t.Fatalf("unsanitized or missing recovery file: %v", err)
	}
	for _, name := range []string{file, filepath.Dir(file)} {
		info, err := os.Stat(name)
		if err != nil || info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("recovery file/directory is not private: %s %v", name, err)
		}
	}
	var manifest api.IssueSourceMapManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	got, err := issues.CompileSourceMaps(api.PublishIssueSourceMapsRequest{Directory: set.Request.Directory, Manifest: manifest})
	if err != nil || got.Digest != set.Digest {
		t.Fatalf("recovery changed manifest identity: %v", err)
	}
}

func TestDeployIssueSourceMapsIntentIdentity(t *testing.T) {
	intent := deployIdempotencyIntent{Slug: "exports", SourceSHA256: "same-archive"}
	first, err := deployIdempotencyKey("", intent)
	if err != nil {
		t.Fatal(err)
	}
	intent.IssueSourceMapsSHA256 = "different-manifest-or-runtime-directory"
	second, err := deployIdempotencyKey("", intent)
	if err != nil || second == first {
		t.Fatalf("publication intent reused deploy identity: %v", err)
	}
	if _, err := publishDeployIssueSourceMaps(context.Background(), nil, "", api.DeploymentResponse{}, "", "", nil); err != nil {
		t.Fatal(err)
	}
	_, set := deploySourceMapsFixture(t)
	for _, deployment := range []api.DeploymentResponse{
		{ID: sourceMapsDeploymentID, TrafficPercent: 100, SourceSHA256: "expected"},
		{ID: sourceMapsDeploymentID, SourceSHA256: "different"},
		{ID: sourceMapsDeploymentID, SourceSHA256: "expected", SourceRoot: "different"},
		{ID: sourceMapsDeploymentID}, {ID: "invalid", SourceSHA256: "expected"},
	} {
		_, err := publishDeployIssueSourceMaps(context.Background(), nil, "exports", deployment, "expected", "", set)
		var binding *deployIssueSourceMapsBindingError
		if !errors.As(err, &binding) {
			t.Fatalf("accepted unsafe deployment binding: %+v (%v)", deployment, err)
		}
	}
}
