package apidserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type submissionCacheFaultStore struct{ MemStore *state.MemStore }

func (*submissionCacheFaultStore) PutIdempotent(context.Context, string, string, int, []byte) error {
	return errors.New("HTTP replay cache unavailable")
}

type submissionAliasFaultStore struct{ MemStore *state.MemStore }

func (*submissionAliasFaultStore) AttachDeploymentSubmission(context.Context, string, string, string) (api.DeploymentSubmissionReceipt, error) {
	return api.DeploymentSubmissionReceipt{}, errors.New("receipt storage unavailable")
}

type discardSubmissionReply struct{ http.ResponseWriter }

func (discardSubmissionReply) WriteHeader(int)                {}
func (discardSubmissionReply) Write(body []byte) (int, error) { return len(body), nil }

func TestDeploymentSubmissionLostReplyAllTransports(t *testing.T) {
	for _, mode := range []string{"image", "multipart", "source-ref", "source-tarball"} {
		t.Run(mode, func(t *testing.T) {
			e := newSourceRefTestServer(t, api.PlanPro, "receipt", 123)
			e.gh.streamBody = io.NopCloser(bytes.NewReader(e.tarball))
			e.gh.streamSHA = strings.Repeat("a", 40)
			e.srv.store = statetest.NewMem(&submissionCacheFaultStore{MemStore: e.store})
			h := e.srv.handler()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					h.ServeHTTP(discardSubmissionReply{w}, r)
					connection, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = connection.Close()
					return
				}
				h.ServeHTTP(w, r)
			}))
			defer srv.Close()
			id := strings.Repeat("c", 32)
			client := api.NewClient(srv.URL, e.key).SetCompletionCache(nil)
			ctx := api.ContextWithDeploymentAttempt(api.ContextWithIdempotencyKey(t.Context(), "lost-reply"), id)
			var err error
			switch mode {
			case "image":
				_, err = client.Deploy(ctx, "receipt", api.CreateDeploymentRequest{Image: "ghcr.io/onebox-faas/hello@sha256:" + strings.Repeat("a", 64)})
			case "multipart":
				_, err = client.DeployMultipart(ctx, "receipt", bytes.NewReader(e.tarball), "src.tar.gz", "node22", "", false, api.DeployAnnotations{})
			case "source-ref":
				_, err = client.DeployFromSourceRef(ctx, "receipt", api.SourceRefDeployRequest{Repo: "onebox-faas/hello", Ref: "main", Format: "tarball"})
			case "source-tarball":
				_, err = client.DeployFromSourceTarball(ctx, "receipt", bytes.NewReader(e.tarball), "src.tar.gz", api.SourceTarballDeployRequest{})
			}
			if err == nil {
				t.Fatal("lost reply succeeded")
			}
			receipt, err := api.NewClient(srv.URL, e.key).GetDeploymentSubmission(t.Context(), "receipt", id)
			if err != nil || receipt.AppID != e.appID || receipt.AttemptID != id || receipt.DeploymentID == "" {
				t.Fatalf("receipt=%+v err=%v", receipt, err)
			}
			deps, err := e.store.ListDeploymentsForApp(t.Context(), e.appID, 10, 0)
			if err != nil || len(deps) != 1 || deps[0].ID != receipt.DeploymentID {
				t.Fatalf("deployments=%+v err=%v", deps, err)
			}
			if mode != "image" && deps[0].BuildID == "" {
				t.Fatalf("source build missing: %+v", deps[0])
			}
			// Even with an incomplete HTTP replay reservation, the same attempt
			// returns its original deployment after it fails.
			if err := e.store.UpdateDeploymentStatus(t.Context(), receipt.DeploymentID, state.DeployFailed, "fixture"); err != nil {
				t.Fatal(err)
			}
			dep, err := client.Deploy(ctx, "receipt", api.CreateDeploymentRequest{Image: "different/image:tag"})
			// The test transport discards all POST replies, so inspect the store.
			_ = dep
			_ = err
			deps, err = e.store.ListDeploymentsForApp(t.Context(), e.appID, 10, 0)
			if err != nil || len(deps) != 1 || deps[0].Status != state.DeployFailed {
				t.Fatalf("replayed attempt created deployment: %+v err=%v", deps, err)
			}
		})
	}
}

func TestDeploymentSubmissionCacheReplayAliasesAndTerminalAttempt(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "receipt"}, nil)
	path := "/v1/apps/receipt/deployments"
	post := func(attempt string) *httptest.ResponseRecorder {
		headers := map[string]string{"Idempotency-Key": "shared-intent"}
		if attempt != "" {
			headers[api.DeploymentAttemptHeader] = attempt
		}
		return e.do(t, "POST", path, api.CreateDeploymentRequest{Image: "ghcr.io/onebox-faas/hello@sha256:" + strings.Repeat("a", 64)}, headers)
	}
	first := post("")
	if first.Code != 202 {
		t.Fatalf("first=%d %s", first.Code, first.Body)
	}
	var dep api.DeploymentResponse
	_ = json.Unmarshal(first.Body.Bytes(), &dep)
	aliasID := strings.Repeat("b", 32)
	alias := post(aliasID)
	if alias.Code != 202 || alias.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("alias=%d %s", alias.Code, alias.Body)
	}
	r := e.do(t, "GET", "/v1/apps/receipt/deployment-submissions/"+aliasID, nil, nil)
	var receipt api.DeploymentSubmissionReceipt
	_ = json.Unmarshal(r.Body.Bytes(), &receipt)
	if r.Code != 200 || receipt.DeploymentID != dep.ID {
		t.Fatalf("alias receipt=%d %s", r.Code, r.Body)
	}
	if err := e.store.UpdateDeploymentStatus(t.Context(), dep.ID, state.DeployFailed, "fixture"); err != nil {
		t.Fatal(err)
	}
	replayed := post(aliasID)
	var terminal api.DeploymentResponse
	_ = json.Unmarshal(replayed.Body.Bytes(), &terminal)
	if replayed.Code != 202 || terminal.ID != dep.ID || terminal.Status != "failed" {
		t.Fatalf("terminal=%d %s", replayed.Code, replayed.Body)
	}
	// A different attempt still preserves legacy stale-key retry behavior.
	next := post(strings.Repeat("c", 32))
	_ = json.Unmarshal(next.Body.Bytes(), &terminal)
	if next.Code != 202 || terminal.ID == dep.ID {
		t.Fatalf("fresh=%d %s", next.Code, next.Body)
	}
}

func TestDeploymentSubmissionValidationOwnershipAndScope(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "receipt"}, nil)
	for _, id := range []string{"", "bad", strings.Repeat("A", 32), strings.Repeat("a", 33)} {
		if id == "" {
			continue
		}
		r := e.do(t, "POST", "/v1/apps/receipt/deployments", api.CreateDeploymentRequest{Image: "ghcr.io/onebox-faas/hello@sha256:" + strings.Repeat("a", 64)}, map[string]string{api.DeploymentAttemptHeader: id})
		if r.Code != 400 {
			t.Fatalf("invalid %q: %d %s", id, r.Code, r.Body)
		}
	}
	id := strings.Repeat("d", 32)
	r := e.do(t, "POST", "/v1/apps/receipt/deployments", api.CreateDeploymentRequest{Image: "ghcr.io/onebox-faas/hello@sha256:" + strings.Repeat("a", 64)}, map[string]string{api.DeploymentAttemptHeader: id})
	if r.Code != 202 {
		t.Fatalf("create=%d %s", r.Code, r.Body)
	}
	other, err := e.store.CreateAccount(t.Context(), "other-receipts@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	key, hash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(t.Context(), other.ID, hash, "other", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/v1/apps/receipt/deployment-submissions/"+id, nil)
	request.Header.Set("Authorization", "Bearer "+key)
	response := httptest.NewRecorder()
	e.h.ServeHTTP(response, request)
	if response.Code != 404 {
		t.Fatalf("IDOR=%d %s", response.Code, response.Body)
	}
	readKey, readHash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, readHash, "read", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+readKey)
	response = httptest.NewRecorder()
	e.h.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("read scope=%d %s", response.Code, response.Body)
	}
	for _, path := range []string{"/v1/apps/receipt/deployment-submissions/" + strings.Repeat("e", 32), "/v1/apps/missing/deployment-submissions/" + id} {
		r := e.do(t, "GET", path, nil, nil)
		if r.Code != 404 {
			t.Fatalf("missing=%d %s", r.Code, r.Body)
		}
	}
}

func TestDeploymentSubmissionCachedReceiptFailure(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "receipt"}, nil)
	request := api.CreateDeploymentRequest{Image: "ghcr.io/onebox-faas/hello@sha256:" + strings.Repeat("a", 64)}
	path := "/v1/apps/receipt/deployments"
	first := e.do(t, "POST", path, request, map[string]string{"Idempotency-Key": "cached-intent"})
	if first.Code != http.StatusAccepted {
		t.Fatalf("first=%d %s", first.Code, first.Body)
	}
	var dep api.DeploymentResponse
	_ = json.Unmarshal(first.Body.Bytes(), &dep)
	e.s.store = statetest.NewMem(&submissionAliasFaultStore{MemStore: e.store})
	e.h = e.s.handler()
	id := strings.Repeat("f", 32)
	response := e.do(t, "POST", path, request, map[string]string{"Idempotency-Key": "cached-intent", api.DeploymentAttemptHeader: id})
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unrecorded replay=%d %s", response.Code, response.Body)
	}
	if _, err := e.store.DeploymentSubmission(t.Context(), dep.AppID, id); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("alias was saved despite fault: %v", err)
	}
	deps, err := e.store.ListDeploymentsForApp(t.Context(), dep.AppID, 10, 0)
	if err != nil || len(deps) != 1 || deps[0].ID != dep.ID {
		t.Fatalf("failed replay created deployment: %+v err=%v", deps, err)
	}
}

func TestDeploymentSubmissionInFlightRemainsUncertain(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "receipt"}, nil)
	path := "/v1/apps/receipt/deployments"
	key := "POST " + path + "\npending-intent"
	reservation, err := e.store.ReserveIdempotent(t.Context(), e.acct.ID, key, idempotencyAbandonAfter)
	if err != nil || !reservation.Reserved {
		t.Fatalf("reservation=%+v err=%v", reservation, err)
	}
	id := strings.Repeat("a", 32)
	r := e.do(t, "POST", path, api.CreateDeploymentRequest{Image: "ghcr.io/onebox-faas/hello@sha256:" + strings.Repeat("a", 64)}, map[string]string{"Idempotency-Key": "pending-intent", api.DeploymentAttemptHeader: id})
	var problem api.Problem
	_ = json.Unmarshal(r.Body.Bytes(), &problem)
	if r.Code != 409 || problem.Code != api.CodeDeploymentAttemptPending {
		t.Fatalf("in-flight=%d %s", r.Code, r.Body)
	}
	app, err := e.store.AppBySlug(t.Context(), "receipt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DeploymentSubmission(t.Context(), app.ID, id); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("in-flight receipt=%v", err)
	}
	deps, err := e.store.ListDeploymentsForApp(t.Context(), app.ID, 10, 0)
	if err != nil || len(deps) != 0 {
		t.Fatalf("in-flight deployed: %+v err=%v", deps, err)
	}
}
