package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestStartPendingSubmissionRecoversWithoutSource(t *testing.T) {
	for _, update := range []bool{false, true} {
		for _, scope := range []string{"default", "staging"} {
			for _, state := range []string{"committed", "open"} {
				t.Run(scope+"/"+state+"/update="+strconv.FormatBool(update), func(t *testing.T) {
					startTestEnvironment(t)
					source := startTestSource(t)
					slug := "first-app"
					if scope != "default" {
						slug = "shop-api"
					}
					var commits atomic.Int32
					startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
						if startTestProjectResponse(w, r) {
							return
						}
						switch r.URL.Path {
						case "/v1/apps/" + slug:
							_ = json.NewEncoder(w).Encode(api.AppResponse{ID: startResponseAppID, Slug: slug})
						case "/v1/uploads/upload-1":
							id := startResponseDeploymentID
							upload := api.UploadSessionResponse{UploadID: "upload-1", AppSlug: slug, TotalSize: 123, ReceivedBytes: 123, Status: state}
							if state == "committed" {
								upload.DeploymentID = &id
							}
							_ = json.NewEncoder(w).Encode(upload)
						case "/v1/uploads/upload-1/commit":
							commits.Add(1)
							if r.Method != http.MethodPost || r.Header.Get("Idempotency-Key") != deployOperationIdempotencyKey("pending-key", "resumable-commit-upload-1") {
								t.Error("recovery changed the commit operation")
							}
							_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: startResponseDeploymentID, AppID: startResponseAppID, Scope: scope, Status: statusLive})
						case "/v1/deployments/" + startResponseDeploymentID:
							_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: startResponseDeploymentID, AppID: startResponseAppID, Scope: scope, Status: statusLive})
						default:
							t.Errorf("recovery submitted new work: %s %s", r.Method, r.URL.Path)
							http.NotFound(w, r)
						}
						if r.Method != http.MethodGet && !strings.HasSuffix(r.URL.Path, "/commit") {
							t.Error("recovery wrote app configuration or source")
						}
					})
					runner := startTestRunner(t, source, "")
					runner.session.AppID, runner.session.AppSlug = startResponseAppID, slug
					if scope != "default" {
						runner.session.Project, runner.session.ProjectID = "shop", "project-1"
						runner.session.Environment, runner.session.EnvironmentID = scope, "env-1"
					}
					if update {
						runner.session.DeploymentID, runner.session.VerifiedDeploymentID = "previous-deployment", "previous-deployment"
					}
					if err := runner.recordStartSubmission(deploySubmission{Key: "pending-key", Transport: "resumable", AppID: startResponseAppID, TotalSize: 123}); err != nil {
						t.Fatal(err)
					}
					if err := runner.recordStartUpload(deployUploadIdentity{UploadID: "upload-1", TotalSize: 123}); err != nil {
						t.Fatal(err)
					}
					if err := os.RemoveAll(source); err != nil {
						t.Fatal(err)
					}
					recovered := startTestRunner(t, source, "\n")
					resume, proceed, err := recovered.chooseRecovery()
					if err != nil || !resume || !proceed {
						t.Fatalf("pending launch was treated as fresh: resume=%t proceed=%t err=%v", resume, proceed, err)
					}
					if err := recovered.recoverStartSubmission(); err != nil {
						t.Fatal(err)
					}
					saved, found, err := loadStartSession(runner.statePath)
					if err != nil || !found || saved.PendingSubmission != nil || saved.DeploymentID != startResponseDeploymentID || saved.Version != 1 || saved.VerifiedDeploymentID != runner.session.VerifiedDeploymentID || !saved.RequireDeploymentEvidence {
						t.Fatalf("recovered the wrong deployment: saved=%+v err=%v", saved, err)
					}
					if (commits.Load() == 1) != (state == "open") {
						t.Fatal("committed recovery wrote again or an incomplete commit used another upload")
					}
				})
			}
		}
	}
}

func TestStartPendingSubmissionIsDurableBeforeLostCommitReply(t *testing.T) {
	startTestEnvironment(t)
	source := startTestSource(t)
	withCwd(t, source)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var created, committed atomic.Bool
	var size atomic.Int64
	var runner *startRunner
	startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/apps/first-app" && created.Load():
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: startResponseAppID, Slug: "first-app"})
		case r.URL.Path == "/v1/apps" && r.Method == http.MethodPost:
			created.Store(true)
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: startResponseAppID, Slug: "first-app"})
		case r.URL.Path == "/v1/uploads" && r.Method == http.MethodPost:
			saved, _, err := loadStartSession(runner.statePath)
			if err != nil || saved.PendingSubmission == nil || saved.Version != startSessionVersion {
				t.Error("source upload started before its pending record was durable")
				w.WriteHeader(500)
				return
			}
			var request struct {
				TotalSize int64 `json:"total_size"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			size.Store(request.TotalSize)
			_ = json.NewEncoder(w).Encode(map[string]any{"upload_id": "upload-1", "chunk_size": 65536, "total_size": request.TotalSize})
		case r.URL.Path == "/v1/uploads/upload-1" && r.Method == http.MethodPatch:
			saved, _, err := loadStartSession(runner.statePath)
			if err != nil || saved.PendingSubmission == nil || saved.PendingSubmission.UploadID != "upload-1" {
				t.Error("upload bytes preceded durable upload identity")
			}
			n, _ := io.Copy(io.Discard, r.Body)
			offset, _ := strconv.ParseInt(r.Header.Get("Upload-Offset"), 10, 64)
			w.Header().Set("Upload-Offset", strconv.FormatInt(offset+n, 10))
		case r.URL.Path == "/v1/uploads/upload-1/commit":
			committed.Store(true)
			// Commit carries a non-replayable empty JSON body; the server only
			// observes the client's disconnect once that body is consumed.
			_, _ = io.Copy(io.Discard, r.Body)
			cancel() // The server accepted it, but the CLI never receives its ID.
			<-r.Context().Done()
		case r.URL.Path == "/v1/uploads/upload-1" && r.Method == http.MethodGet:
			id := startResponseDeploymentID
			_ = json.NewEncoder(w).Encode(api.UploadSessionResponse{UploadID: "upload-1", AppSlug: "first-app", TotalSize: size.Load(), ReceivedBytes: size.Load(), Status: "committed", DeploymentID: &id})
		case r.URL.Path == "/v1/deployments/"+startResponseDeploymentID:
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: startResponseDeploymentID, AppID: startResponseAppID, Scope: "default", Status: statusLive})
		default:
			http.NotFound(w, r)
		}
	})
	runner = startTestRunner(t, source, "y\n")
	runner.ctx, runner.deploy = ctx, cmdDeployTarballToExisting
	code, submitted := runner.reviewAndDeploy()
	if code == 0 || submitted || !committed.Load() {
		t.Fatalf("lost commit response did not interrupt the launch: code=%d submitted=%t", code, submitted)
	}
	// Even losing the generic upload cache must not lose Start's identity.
	if err := os.RemoveAll(filepath.Join(os.Getenv("XDG_STATE_HOME"), "gregale", "uploads")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	recovered := startTestRunner(t, source, "\n")
	if resume, proceed, err := recovered.chooseRecovery(); err != nil || !resume || !proceed {
		t.Fatalf("lost submission not recoverable: %v", err)
	}
	if err := recovered.recoverStartSubmission(); err != nil || recovered.session.DeploymentID != startResponseDeploymentID {
		t.Fatalf("lost commit did not recover: %v", err)
	}
}

func TestStartPendingSubmissionRejectsUncertainOrMismatchedResults(t *testing.T) {
	for _, outcome := range []string{"legacy", "missing upload", "wrong upload", "wrong app", "wrong size", "missing deployment", "wrong deployment", "wrong scope", "app replaced", "incomplete", "unavailable", "contradictory"} {
		t.Run(outcome, func(t *testing.T) {
			startTestEnvironment(t)
			startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error("uncertain recovery mutated state")
				}
				id := startResponseDeploymentID
				switch r.URL.Path {
				case "/v1/apps/first-app":
					appID := startResponseAppID
					if outcome == "app replaced" {
						appID = "replacement"
					}
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: appID})
				case "/v1/uploads/upload-1":
					if outcome == "missing upload" {
						http.NotFound(w, r)
						return
					}
					if outcome == "unavailable" {
						w.WriteHeader(503)
						return
					}
					upload := api.UploadSessionResponse{UploadID: "upload-1", AppSlug: "first-app", TotalSize: 123, ReceivedBytes: 123, Status: "committed", DeploymentID: &id}
					switch outcome {
					case "wrong upload":
						upload.UploadID = "other"
					case "wrong app":
						upload.AppSlug = "other"
					case "wrong size":
						upload.TotalSize++
					case "missing deployment":
						upload.DeploymentID = nil
					case "incomplete":
						upload.Status, upload.ReceivedBytes = "open", 1
						upload.DeploymentID = nil
					case "contradictory":
						upload.Status = "cancelled"
					}
					_ = json.NewEncoder(w).Encode(upload)
				case "/v1/deployments/" + id:
					dep := api.DeploymentResponse{ID: id, AppID: startResponseAppID, Scope: "default"}
					if outcome == "wrong deployment" {
						dep.ID = "other"
					}
					if outcome == "wrong scope" {
						dep.Scope = "staging"
					}
					_ = json.NewEncoder(w).Encode(dep)
				default:
					t.Errorf("unexpected recovery route: %s", r.URL.Path)
				}
			})
			runner := startTestRunner(t, startTestSource(t), "n\n")
			if err := runner.recordStartSubmission(deploySubmission{Key: "pending-key", Transport: "resumable", AppID: startResponseAppID, TotalSize: 123}); err != nil {
				t.Fatal(err)
			}
			if err := runner.recordStartUpload(deployUploadIdentity{UploadID: "upload-1", TotalSize: 123}); err != nil {
				t.Fatal(err)
			}
			if outcome == "legacy" {
				runner.session.PendingSubmission.Transport = "multipart"
				if err := saveStartSession(runner.statePath, runner.session); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(runner.statePath)
			if err := runner.recoverStartSubmission(); err == nil {
				t.Fatal("uncertain submission was cleared or verified")
			}
			after, _ := os.ReadFile(runner.statePath)
			if !bytes.Equal(before, after) || runner.session.PendingSubmission == nil {
				t.Fatal("failed recovery lost its record")
			}
			if code, _ := runner.reviewAndDeploy(); code == 0 {
				t.Fatal("uncertain submission permitted another deployment")
			}
		})
	}
}

func TestStartUploadIdentitySaveFailureStopsBeforeCommit(t *testing.T) {
	startTestEnvironment(t)
	path := filepath.Join(t.TempDir(), "source.tar.gz")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/uploads" {
			t.Error("failed identity save sent upload bytes or committed")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"upload_id": "upload-1", "chunk_size": 3, "total_size": 7})
	})
	_, _, _, err := deployResumableTarball(api.NewClient(apiBase(), loadToken()), t.Context(), "first-app", path, nil, func(deployUploadIdentity) error { return errors.New("fixture disk failure") })
	if err == nil || !strings.Contains(err.Error(), "fixture disk failure") {
		t.Fatalf("identity persistence failure was ignored: %v", err)
	}
}

func TestStartPendingSubmissionVersionAndFinishKeepRecord(t *testing.T) {
	startTestEnvironment(t)
	runner := startTestRunner(t, startTestSource(t), "2\n")
	if err := runner.recordStartSubmission(deploySubmission{Key: "pending-key", Transport: "multipart", AppID: startResponseAppID, TotalSize: 123}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(runner.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if resume, proceed, err := runner.chooseRecovery(); err != nil || resume || proceed {
		t.Fatalf("finishing pending recovery resumed work: %v", err)
	}
	after, _ := os.ReadFile(runner.statePath)
	if !bytes.Equal(before, after) {
		t.Fatal("finishing changed the pending record")
	}
	var oldFormat map[string]any
	if err := json.Unmarshal(before, &oldFormat); err != nil {
		t.Fatal(err)
	}
	oldFormat["version"] = 1
	wire, err := json.Marshal(oldFormat)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runner.statePath, wire, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadStartSession(runner.statePath); err == nil {
		t.Fatal("a pending submission was accepted in a format older CLIs silently ignore")
	}
}

func TestStartSubmissionFailureClearsOnlyConfirmedNonAcceptance(t *testing.T) {
	for _, stage := range []string{"upload", "submission", "deployment"} {
		t.Run(stage, func(t *testing.T) {
			startTestEnvironment(t)
			runner := startTestRunner(t, startTestSource(t), "")
			if err := runner.recordStartSubmission(deploySubmission{Key: "pending-key", Transport: "resumable", AppID: startResponseAppID, TotalSize: 123}); err != nil {
				t.Fatal(err)
			}
			if err := runner.recordStartUpload(deployUploadIdentity{UploadID: "upload-1", TotalSize: 123}); err != nil {
				t.Fatal(err)
			}
			runner.recordStartSubmissionFailure(&deployPhaseError{stage: stage, err: &APIError{Problem: api.Problem{Status: 404}}})
			saved, _, err := loadStartSession(runner.statePath)
			if err != nil || (saved.PendingSubmission != nil) != (stage != "upload") {
				t.Fatalf("accepted deployment evidence was cleared or a rejected upload was kept: %+v %v", saved, err)
			}
		})
	}
}

func TestStartPendingRecoverySaveFailurePreservesIdentity(t *testing.T) {
	startTestEnvironment(t)
	startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("save failure submitted work")
		}
		switch r.URL.Path {
		case "/v1/apps/first-app":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: startResponseAppID})
		case "/v1/uploads/upload-1":
			id := startResponseDeploymentID
			_ = json.NewEncoder(w).Encode(api.UploadSessionResponse{UploadID: "upload-1", AppSlug: "first-app", TotalSize: 123, ReceivedBytes: 123, Status: "committed", DeploymentID: &id})
		case "/v1/deployments/" + startResponseDeploymentID:
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: startResponseDeploymentID, AppID: startResponseAppID, Scope: "default"})
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	})
	runner := startTestRunner(t, startTestSource(t), "")
	if err := runner.recordStartSubmission(deploySubmission{Key: "pending-key", Transport: "resumable", AppID: startResponseAppID, TotalSize: 123}); err != nil {
		t.Fatal(err)
	}
	if err := runner.recordStartUpload(deployUploadIdentity{UploadID: "upload-1", TotalSize: 123}); err != nil {
		t.Fatal(err)
	}
	path := runner.statePath
	before, _ := os.ReadFile(path)
	runner.statePath = filepath.Join(path, "unwritable.json")
	if err := runner.recoverStartSubmission(); err == nil || runner.session.PendingSubmission == nil {
		t.Fatal("failed save dropped the pending identity")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("failed recovery save changed the original record")
	}
	runner.statePath = path
	if err := runner.recoverStartSubmission(); err != nil {
		t.Fatal(err)
	}
}

func TestStartPendingCancellationCannotDiscardAcceptedDeployment(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(strconv.FormatBool(committed), func(t *testing.T) {
			startTestEnvironment(t)
			var cancellations atomic.Int32
			startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/apps/first-app":
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: startResponseAppID})
				case "/v1/uploads/upload-1":
					if r.Method == http.MethodDelete {
						if committed {
							t.Error("accepted upload was cancelled")
						}
						cancellations.Add(1)
						w.WriteHeader(http.StatusNoContent)
						return
					}
					upload := api.UploadSessionResponse{UploadID: "upload-1", AppSlug: "first-app", TotalSize: 123, ReceivedBytes: 123, Status: "open"}
					if cancellations.Load() > 0 {
						upload.Status = "cancelled"
					}
					if committed {
						id := startResponseDeploymentID
						upload.Status, upload.DeploymentID = "committed", &id
					}
					_ = json.NewEncoder(w).Encode(upload)
				case "/v1/deployments/" + startResponseDeploymentID:
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: startResponseDeploymentID, AppID: startResponseAppID, Scope: "default"})
				default:
					t.Errorf("cancellation submitted work: %s %s", r.Method, r.URL.Path)
				}
			})
			runner := startTestRunner(t, startTestSource(t), "3\n")
			if err := runner.recordStartSubmission(deploySubmission{Key: "pending-key", Transport: "resumable", AppID: startResponseAppID, TotalSize: 123}); err != nil {
				t.Fatal(err)
			}
			if err := runner.recordStartUpload(deployUploadIdentity{UploadID: "upload-1", TotalSize: 123}); err != nil {
				t.Fatal(err)
			}
			if resume, proceed, err := runner.chooseRecovery(); err != nil || !resume || !proceed {
				t.Fatalf("cancellation did not resolve its recorded upload: %v", err)
			}
			err := runner.recoverStartSubmission()
			if (err == nil) != committed || runner.session.PendingSubmission != nil || (cancellations.Load() == 0) != committed || (runner.session.DeploymentID == startResponseDeploymentID) != committed {
				t.Fatalf("wrong cancellation outcome: committed=%t cancels=%d session=%+v err=%v", committed, cancellations.Load(), runner.session, err)
			}
		})
	}
}

func TestStartPendingSubmissionDoesNotRetryPreviousFailedBuild(t *testing.T) {
	stdout, _ := startTestEnvironment(t)
	runner := startTestRunner(t, startTestSource(t), "")
	runner.session.DeploymentID, runner.session.Status = "previous-failure", deploymentStatusFailed
	if err := runner.recordStartSubmission(deploySubmission{Key: "pending-key", Transport: "multipart", AppID: startResponseAppID, TotalSize: 123}); err != nil {
		t.Fatal(err)
	}
	if code, ready := runner.recoverFailure(1); code != 1 || ready || strings.Contains(stdout.String(), "The deployment failed.") {
		t.Fatal("uncertain submission offered a retry of the previous failed build")
	}
}

func TestStartCachedUploadDiscoveryPreservesUnknownOutcome(t *testing.T) {
	for _, outcome := range []string{"missing", "unknown", "unavailable", "contradictory"} {
		t.Run(outcome, func(t *testing.T) {
			startTestEnvironment(t)
			var runner *startRunner
			startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/uploads/upload-1" {
					t.Error("unknown cached upload started another submission")
				}
				saved, _, err := loadStartSession(runner.statePath)
				if err != nil || saved.PendingSubmission == nil || saved.PendingSubmission.UploadID != "upload-1" {
					t.Error("cached upload was queried before its identity was durable")
				}
				if outcome == "missing" {
					http.NotFound(w, r)
					return
				}
				if outcome == "unavailable" {
					w.WriteHeader(503)
					return
				}
				upload := api.UploadSessionResponse{UploadID: "upload-1", AppSlug: "first-app", TotalSize: 7, ReceivedBytes: 7, Status: "unknown"}
				if outcome == "contradictory" {
					id := startResponseDeploymentID
					upload.Status, upload.DeploymentID = "cancelled", &id
				}
				_ = json.NewEncoder(w).Encode(upload)
			})
			path := filepath.Join(t.TempDir(), "source.tar.gz")
			if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			digest, err := tarballSHA256(path)
			if err != nil {
				t.Fatal(err)
			}
			options, err := deployOptionsFingerprint(nil)
			if err != nil {
				t.Fatal(err)
			}
			client := api.NewClient(apiBase(), loadToken())
			identity := uploadRecoveryIdentity(client, t.Context(), "first-app", digest, options)
			statePath, err := uploadStatePath(identity)
			if err != nil {
				t.Fatal(err)
			}
			if err := saveResumableUploadState(statePath, resumableUploadState{Version: resumableUploadStateVersion, UploadID: "upload-1", AppSlug: "first-app", APIBase: apiBase(), RecoveryIdentitySHA256: identity, ArchivePath: path, ArchiveSHA256: digest, ArchiveSize: 7, TotalSize: 7, ChunkSize: 3, DeployOptionsSHA256: options, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
				t.Fatal(err)
			}
			runner = startTestRunner(t, startTestSource(t), "")
			if err := runner.recordStartSubmission(deploySubmission{Key: "pending-key", Transport: "resumable", AppID: startResponseAppID, TotalSize: 7}); err != nil {
				t.Fatal(err)
			}
			_, _, _, err = deployResumableTarball(client, t.Context(), "first-app", path, nil, runner.recordStartUpload)
			if err == nil {
				t.Fatal("uncertain cached upload did not stop")
			}
			runner.recordStartSubmissionFailure(err)
			saved, _, loadErr := loadStartSession(runner.statePath)
			if loadErr != nil || saved.PendingSubmission == nil || saved.PendingSubmission.UploadID != "upload-1" {
				t.Fatalf("unknown outcome lost its identity: %v", loadErr)
			}
		})
	}
}
