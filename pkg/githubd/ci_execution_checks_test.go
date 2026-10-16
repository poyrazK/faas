package githubd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
)

func executionCICheckFixture() ci.ExecutionCheck {
	return ci.ExecutionCheck{AccountID: uuid.NewString(), ExecutionID: uuid.NewString(), BindingID: uuid.NewString(), Pipeline: "checks",
		InstallationID: 41, RepositoryID: 123, Repository: "gregale/api", CommitSHA: strings.Repeat("a", 40), Version: 1, Status: "queued", Summary: "Bounded job states."}
}

// Signed HTTP ingress -> PostgreSQL admission -> real Checks HTTP -> response
// loss -> restart -> latest terminal Check, with no compute commands executed.
func verifyAutomatedExecutionChecks(t *testing.T, pool *pgxpool.Pool, store *ci.PostgresStore, cfg ci.Config, verify ci.GitHubVerifier, account, execution string) {
	t.Helper()
	creates, patches := 0, 0
	var remote map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-ci-token" {
			t.Error("incorrect token")
		}
		if r.Method == http.MethodGet {
			if strings.Contains(r.URL.Path, "/commits/") {
				if remote == nil {
					_, _ = w.Write([]byte(`{"check_runs":[]}`))
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"check_runs": []any{remote}})
			} else {
				_ = json.NewEncoder(w).Encode(remote)
			}
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if r.Method == http.MethodPost {
			creates++
			remote = payload
			remote["id"] = 77
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{`))
			return
		}
		patches++
		for key, value := range payload {
			remote[key] = value
		}
		_, _ = w.Write([]byte(`{"id":77}`))
	}))
	t.Cleanup(server.Close)
	tokens := NewTokenCache(fakeFetcher(func(_ context.Context, id int64) (string, time.Time, error) {
		if id != 12 {
			t.Error("foreign installation token requested")
		}
		return "synthetic-ci-token", time.Now().Add(time.Hour), nil
	}), time.Minute)
	writer := &ChecksAPI{Tokens: tokens, HTTP: &singleHostClient{base: server.Client(), api: server.URL}}
	if worked, err := ci.PublishExecutionCheckOnce(t.Context(), cfg, store, verify, writer.WriteExecutionCICheck); !worked || err == nil {
		t.Fatal("lost create response did not retain retry")
	}
	if _, err := store.CancelExecution(t.Context(), account, execution); err != nil {
		t.Fatal(err)
	}
	store = ci.NewPostgresStore(pool)
	if _, err := pool.Exec(t.Context(), "UPDATE ci_execution_checks SET status='dead',attempts=$2,last_error='unavailable' WHERE execution_id=$1", execution, api.CIGitHubMaxAttempts); err != nil {
		t.Fatal(err)
	}
	retry, replay, err := ci.RequestCheckRetry(t.Context(), cfg, store, verify, account, execution, "http-response-loss-retry", ci.CheckRetryRequest{})
	if err != nil || replay {
		t.Fatalf("recovery request: %v", err)
	}
	store = ci.NewPostgresStore(pool)
	again, replay, err := ci.RequestCheckRetry(t.Context(), cfg, store, verify, account, execution, "http-response-loss-retry", ci.CheckRetryRequest{})
	if err != nil || !replay || again.ID != retry.ID {
		t.Fatal("restart duplicated retry intent")
	}
	if worked, err := ci.PublishExecutionCheckOnce(t.Context(), cfg, store, verify, writer.WriteExecutionCICheck); !worked || err != nil {
		t.Fatalf("delivery recovery failed: %v", err)
	}
	if creates != 1 || patches != 1 || remote["status"] != "completed" || remote["conclusion"] != "cancelled" {
		t.Fatal("recovery duplicated Check or replayed queued state")
	}
	var synced bool
	if err := pool.QueryRow(t.Context(), `SELECT c.status='sent' AND c.check_id=77 AND c.delivered_version=e.version
 FROM ci_execution_checks c JOIN ci_executions e ON e.id=c.execution_id WHERE e.id=$1`, execution).Scan(&synced); err != nil || !synced {
		t.Fatalf("delivery receipt lost: %v", err)
	}
}

func executionCICheckWriter(t *testing.T, handler http.HandlerFunc) *ChecksAPI {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	tokens := NewTokenCache(fakeFetcher(func(_ context.Context, id int64) (string, time.Time, error) {
		if id != 41 {
			t.Error("foreign installation token requested")
		}
		return "synthetic-ci-token", time.Now().Add(time.Hour), nil
	}), time.Minute)
	return &ChecksAPI{Tokens: tokens, HTTP: &singleHostClient{base: server.Client(), api: server.URL}}
}

func TestCIExecutionCheckHTTPRecoversCreateAndUpdateAcknowledgements(t *testing.T) {
	check := executionCICheckFixture()
	creates, patches := 0, 0
	var remote map[string]any
	loseUpdate := false
	writer := executionCICheckWriter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-ci-token" {
			t.Error("missing installation token")
		}
		if r.Method == http.MethodGet {
			if strings.Contains(r.URL.Path, "/commits/") {
				if remote == nil {
					_, _ = w.Write([]byte(`{"check_runs":[]}`))
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"check_runs": []any{remote}})
			} else {
				_ = json.NewEncoder(w).Encode(remote)
			}
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload["external_id"] != check.ExternalID() || payload["name"] != check.Name() {
			t.Error("execution identity changed")
		}
		if r.Method == http.MethodPost {
			creates++
			remote = payload
			remote["id"] = float64(77)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{`))
			return
		}
		patches++
		if r.URL.Path != "/repos/gregale/api/check-runs/77" || payload["head_sha"] != nil {
			t.Error("PATCH did not target original Check")
		}
		for key, value := range payload {
			remote[key] = value
		}
		if loseUpdate {
			loseUpdate = false
			_, _ = w.Write([]byte(`{`))
			return
		}
		_, _ = w.Write([]byte(`{"id":77}`))
	})
	if _, err := writer.WriteExecutionCICheck(t.Context(), check); !errors.Is(err, ci.ErrGitHubUnavailable) {
		t.Fatalf("lost POST response: %v", err)
	}
	check.Version, check.Status = 3, "in_progress"
	if id, err := writer.WriteExecutionCICheck(t.Context(), check); err != nil || id != 77 {
		t.Fatalf("recover POST: %v", err)
	}
	check.CheckID, check.Version, check.Status, check.Conclusion = 77, 5, "completed", "success"
	loseUpdate = true
	if _, err := writer.WriteExecutionCICheck(t.Context(), check); !errors.Is(err, ci.ErrGitHubUnavailable) {
		t.Fatal("lost PATCH response did not retry")
	}
	if id, err := writer.WriteExecutionCICheck(t.Context(), check); err != nil || id != 77 {
		t.Fatalf("recover PATCH: %v", err)
	}
	if creates != 1 || patches != 3 || remote["status"] != "completed" || remote["conclusion"] != "success" {
		t.Fatal("remote Check duplicated or lost terminal outcome")
	}
	check.Status, check.Conclusion, check.Version = "in_progress", "", 3
	if _, err := writer.WriteExecutionCICheck(t.Context(), check); err == nil || patches != 3 {
		t.Fatal("stale progress overwrote completed Check")
	}
}

func TestCIExecutionCheckHTTPRefusesForeignOrNewerRemoteCheck(t *testing.T) {
	for _, field := range []string{"id", "name", "external_id", "head_sha", "newer", "terminal", "missing_version"} {
		t.Run(field, func(t *testing.T) {
			check := executionCICheckFixture()
			check.CheckID = 77
			remote := map[string]any{"id": 77, "name": check.Name(), "external_id": check.ExternalID(), "head_sha": check.CommitSHA, "status": "queued", "output": map[string]any{"title": check.Title()}}
			switch field {
			case "id":
				remote[field] = 99
			case "name", "external_id", "head_sha":
				remote[field] = "foreign"
			case "newer":
				next := check
				next.Version = 7
				remote["output"] = map[string]any{"title": next.Title()}
			case "terminal":
				remote["status"] = "completed"
			case "missing_version":
				remote["output"] = map[string]any{"title": "foreign title"}
			}
			writer := executionCICheckWriter(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error("foreign or newer Check was patched")
				}
				_ = json.NewEncoder(w).Encode(remote)
			})
			if _, err := writer.WriteExecutionCICheck(t.Context(), check); err == nil {
				t.Fatal("invalid remote Check accepted")
			}
		})
	}
}

func TestCIExecutionCheckHTTPPayloadsAndProjectionValidation(t *testing.T) {
	for _, conclusion := range []string{"success", "failure", "cancelled", "timed_out"} {
		t.Run(conclusion, func(t *testing.T) {
			check := executionCICheckFixture()
			check.Status, check.Conclusion = "completed", conclusion
			writer := executionCICheckWriter(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(`{"check_runs":[]}`))
					return
				}
				var payload checkRunRequest
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if payload.Status != "completed" || payload.Conclusion != conclusion || payload.Output.Summary != check.Summary || payload.DetailsURL != "" {
					t.Error("terminal payload incorrect")
				}
				_, _ = w.Write([]byte(`{"id":77}`))
			})
			if _, err := writer.WriteExecutionCICheck(t.Context(), check); err != nil {
				t.Fatal(err)
			}
		})
	}
	check := executionCICheckFixture()
	writer := executionCICheckWriter(t, func(http.ResponseWriter, *http.Request) { t.Error("invalid projection reached HTTP") })
	check.Status = "completed" // No conclusion cannot accidentally become success.
	if _, err := writer.WriteExecutionCICheck(t.Context(), check); err == nil {
		t.Fatal("incomplete terminal projection accepted")
	}
}
