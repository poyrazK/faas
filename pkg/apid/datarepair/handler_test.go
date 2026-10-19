package datarepair

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	repair "github.com/onebox-faas/faas/pkg/datarepair"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

type testRunner struct{}

func (testRunner) Rehearse(_ context.Context, p repair.Plan) (repair.Evidence, error) {
	return repair.Evidence{Passed: true, PlanDigest: p.Digest, SnapshotSHA256: strings.Repeat("b", 64), Runner: "test-only", ChecksSHA256: p.ChecksSHA256}, nil
}

// ADR-20261016: scope and actor are authenticated, evidence is executor-owned,
// and durable plan receipts settle repeated HTTP apply submissions.
func TestSelectiveRepairAuthenticatedJourney(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, repair.Schema()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE products(id int PRIMARY KEY,category text NOT NULL);SELECT gregale_repair.enroll('products',ARRAY['category']);INSERT INTO products(id,category) VALUES(1,'correct'),(2,'correct')`); err != nil {
		t.Fatal(err)
	}
	s := repair.Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production"}
	op := uuid.NewString()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = repair.Capture(ctx, tx, s, op, "bad-import", "products", func(tx pgx.Tx) error { _, err := tx.Exec(ctx, `UPDATE products SET category='wrong'`); return err }); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("x", 32)
	h := &Handler{Engine: &repair.Engine{Pool: pool, Rehearser: testRunner{}}, Release: "executor-v1", Authenticate: Bearer(token, Principal{Scope: s, Actor: "authenticated-reviewer", CanApprove: true, CanApply: true})}
	server := httptest.NewServer(h.Routes())
	defer server.Close()
	remote, err := NewRemote(server.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	page, err := remote.Sources(ctx, s, repair.SourceQuery{Release: "bad-import"})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != op || page.Items[0].ChangeRows == nil || *page.Items[0].ChangeRows != 2 {
		t.Fatalf("authenticated source discovery: %+v %v", page, err)
	}
	call := func(method, path, body, bearer string) (int, repair.Plan) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		var p repair.Plan
		if res.StatusCode == 200 {
			if err = json.NewDecoder(res.Body).Decode(&p); err != nil {
				t.Fatal(err)
			}
		}
		if res.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("sensitive response is cacheable")
		}
		return res.StatusCode, p
	}
	base := "/v1/data-repair/plans"
	if status, _ := call("GET", "/v1/data-repair/sources", "", ""); status != 401 {
		t.Fatalf("unauthenticated discovery: %d", status)
	}
	for _, query := range []string{"scope=other", "environment=staging", "release=a&release=b", "limit=0", "limit=101", "since=invalid", "release=%ZZ"} {
		if status, _ := call("GET", "/v1/data-repair/sources?"+query, "", token); status != 400 {
			t.Fatalf("invalid discovery query %s: %d", query, status)
		}
	}
	if status, _ := call("POST", base, `{"operation_id":"`+op+`"}`, ""); status != 401 {
		t.Fatalf("unauthenticated access: %d", status)
	}
	if status, _ := call("POST", base, `{"operation_id":"`+op+`","scope":{}}`, token); status != 400 {
		t.Fatalf("caller supplied scope accepted: %d", status)
	}
	status, p := call("POST", base, `{"operation_id":"`+op+`"}`, token)
	if status != 200 || p.Candidates != 2 {
		t.Fatalf("prepare: %d %+v", status, p)
	}
	path := base + "/" + p.ID
	body := `{"digest":"` + p.Digest + `"}`
	if status, _ = call("POST", path+"/approve", body, token); status != 409 {
		t.Fatalf("unrehearsed approval: %d", status)
	}
	if status, _ = call("POST", path+"/rehearse", `{"digest":"`+p.Digest+`","evidence":{"passed":true}}`, token); status != 400 {
		t.Fatalf("forged evidence accepted: %d", status)
	}
	if status, _ = call("POST", path+"/rehearse", body, token); status != 200 {
		t.Fatalf("rehearse: %d", status)
	}
	status, p = call("POST", path+"/approve", body, token)
	if status != 200 || p.ApprovedBy == nil || *p.ApprovedBy != "authenticated-reviewer" {
		t.Fatalf("approval actor: %d %+v", status, p)
	}
	repairID := uuid.NewString()
	applyBody := `{"digest":"` + p.Digest + `","repair_operation_id":"` + repairID + `"}`
	for range 2 {
		status, p = call("POST", path+"/apply", applyBody, token)
		if status != 200 || p.RepairOperationID == nil || *p.RepairOperationID != repairID {
			t.Fatalf("apply/receipt replay: %d %+v", status, p)
		}
	}
	page, err = remote.Sources(ctx, s, repair.SourceQuery{Release: "bad-import"})
	if err != nil || len(page.Items) != 1 || page.Items[0].LatestRepair == nil || page.Items[0].LatestRepair.OperationID != repairID {
		t.Fatalf("discovery lost apply receipt: %+v %v", page, err)
	}
	if status, _ = call("POST", path+"/approve", `{"digest":"`+p.Digest+`","actor":"forged"}`, token); status != 400 {
		t.Fatalf("caller supplied approver accepted: %d", status)
	}
	// Changing authenticated scope cannot expose the earlier plan, including diffs.
	other := s
	other.Environment = "staging"
	h.Authenticate = Bearer(token, Principal{Scope: other, Actor: "other", CanApply: true})
	page, err = remote.Sources(ctx, other, repair.SourceQuery{})
	if err != nil || len(page.Items) != 0 || page.Scope != other {
		t.Fatalf("discovery crossed authenticated scope: %+v %v", page, err)
	}
	if status, _ = call("GET", path, "", token); status != 404 {
		t.Fatalf("cross-scope plan leaked: %d", status)
	}
}

func TestSelectiveRepairShellContainsNoData(t *testing.T) {
	h := &Handler{}
	for _, path := range []string{"/", "/app.js", "/resolution.js"} {
		w := httptest.NewRecorder()
		h.Routes().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || bytes.Contains(w.Body.Bytes(), []byte("localStorage")) {
			t.Fatalf("unsafe review shell: %d", w.Code)
		}
	}
}
