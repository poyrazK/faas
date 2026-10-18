package datarepair

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestRepairResolveAuthenticatedExecutor(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenDatabase(t)
	if _, err := pool.Exec(ctx, core.Schema()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE products(id int PRIMARY KEY,category text NOT NULL);
 SELECT gregale_repair.enroll('products',ARRAY['category']); INSERT INTO products VALUES(1,'original')`); err != nil {
		t.Fatal(err)
	}
	scope := core.Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production"}
	source := uuid.NewString()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := core.Capture(ctx, tx, scope, source, "import", "products", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE products SET category='bad'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	engine := &core.Engine{Pool: pool}
	p, err := engine.Plan(ctx, scope, source)
	if err != nil {
		t.Fatal(err)
	}
	token, reader := strings.Repeat("x", 64), strings.Repeat("r", 64)
	h := &Handler{Engine: engine, Authenticate: func(r *http.Request) (Principal, error) {
		if r.Header.Get("Authorization") == "Bearer "+reader {
			return Bearer(reader, Principal{Scope: scope, Actor: "reader"})(r)
		}
		return Bearer(token, Principal{Scope: scope, Actor: "executor", CanApply: true})(r)
	}}
	server := httptest.NewServer(h.Routes())
	defer server.Close()
	id := uuid.NewString()
	valid := `{"digest":"` + p.Digest + `","repair_operation_id":"` + id + `"}`
	for _, tc := range []struct {
		body, token string
		status      int
	}{
		{valid, "", 401}, {valid, reader, 403},
		{strings.TrimSuffix(valid, "}") + `,"actor":"forged"}`, token, 400},
		{strings.TrimSuffix(valid, "}") + `,"scope":{}}`, token, 400},
		{strings.TrimSuffix(valid, "}") + `,"evidence":{}}`, token, 400},
		{valid + `{}`, token, 400},
		{`{"digest":"` + p.Digest + `"}`, token, 400},
		{strings.Replace(valid, p.Digest, strings.Repeat("0", 64), 1), token, 409},
	} {
		req, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/v1/data-repair/plans/"+p.ID+"/resolve", strings.NewReader(tc.body))
		req.Header.Set("Authorization", "Bearer "+tc.token)
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != tc.status || resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("resolution authorization/strict body", resp.StatusCode, tc.status)
		}
	}
	coordinator := *h
	coordinator.Coordinator = true
	coordinatorServer := httptest.NewServer(coordinator.Routes())
	defer coordinatorServer.Close()
	remote, err := NewRemote(coordinatorServer.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		got, err := remote.Resolve(ctx, scope, p.ID, p.Digest, id, "recorded-approver")
		if err != nil || got.ApplyResolution == nil || !got.ResolvedApply(p.ID, p.Digest, id) {
			t.Fatal("resolution protocol did not return a bound closure", err)
		}
	}
	other := scope
	other.Environment = "staging"
	if _, err := remote.Resolve(ctx, other, p.ID, p.Digest, id, "recorded-approver"); !errors.Is(err, core.ErrUnavailable) {
		t.Fatal("resolution response crossed scope", err)
	}
}

func TestRepairResolveRemoteRejectsUnprovenOutcomes(t *testing.T) {
	id, source, repairID, digest := uuid.NewString(), uuid.NewString(), uuid.NewString(), strings.Repeat("a", 64)
	scope := core.Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production"}
	for _, mode := range []string{"no_receipt", "wrong_identity", "wrong_digest", "wrong_scope", "both_outcomes", "unknown_outcome"} {
		t.Run(mode, func(t *testing.T) {
			p := core.Plan{ID: id, SourceOperationID: source, Scope: scope, Digest: digest,
				ApplyResolution: &core.ApplyResolution{Outcome: core.ResolutionNotCommitted, PlanDigest: digest, RepairOperationID: repairID, ResolvedAt: time.Now().UTC(), ResolvedBy: "resolver"}}
			switch mode {
			case "no_receipt":
				p.ApplyResolution = nil
			case "wrong_identity":
				p.ApplyResolution.RepairOperationID = uuid.NewString()
			case "wrong_digest":
				p.ApplyResolution.PlanDigest = strings.Repeat("b", 64)
			case "wrong_scope":
				p.Scope.Environment = "staging"
			case "both_outcomes":
				at := time.Now().UTC()
				p.AppliedAt = &at
			case "unknown_outcome":
				p.ApplyResolution.Outcome = "probably_rolled_back"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(p) }))
			defer server.Close()
			remote, err := NewRemote(server.URL, strings.Repeat("x", 64))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := remote.Resolve(t.Context(), scope, id, digest, repairID, "resolver"); !errors.Is(err, core.ErrUnavailable) {
				t.Fatal("unproven source result granted closure", err)
			}
		})
	}
}
