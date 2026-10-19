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

func TestRepairVerificationAuthenticatedReadOnlyJourney(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenDatabase(t)
	if _, err := pool.Exec(ctx, core.Schema()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE products(id int PRIMARY KEY,category text NOT NULL);
SELECT gregale_repair.enroll('products',ARRAY['category']); INSERT INTO products VALUES(1,'original'),(2,'original')`); err != nil {
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
	engine := &core.Engine{Pool: pool, Rehearser: testRunner{}}
	p, err := engine.Plan(ctx, scope, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Rehearse(ctx, scope, p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Approve(ctx, scope, p.ID, p.Digest, "reviewer"); err != nil {
		t.Fatal(err)
	}
	p, err = engine.Apply(ctx, scope, p.ID, p.Digest, uuid.NewString(), "repair")
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("v", 32)
	server := httptest.NewServer((&Handler{Engine: engine, Authenticate: Bearer(token, Principal{Scope: scope, Actor: "reader"})}).Routes())
	defer server.Close()
	remote, err := NewRemote(server.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	r, err := remote.Verify(ctx, scope, p.ID, p.Digest)
	if err != nil || !r.MatchesPlan(p) || !r.Available || r.Unchanged != 2 {
		t.Fatal("read-only principal could not verify committed repair", r, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE products SET category='human' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	r, err = remote.Verify(ctx, scope, p.ID, p.Digest)
	if err != nil || r.Unchanged != 1 || r.Edited != 1 || r.Rows[0].KeyJSON != `{"id": 1}` {
		t.Fatal("HTTP verification did not observe later edit", r, err)
	}
	after, err := engine.Get(ctx, scope, p.ID)
	beforeJSON, _ := json.Marshal(p)
	afterJSON, _ := json.Marshal(after)
	if err != nil || string(beforeJSON) != string(afterJSON) {
		t.Fatal("HTTP verification changed the committed plan or approval", err)
	}
	path := "/v1/data-repair/plans/" + p.ID + "/verify"
	for _, tc := range []struct {
		body, token string
		status      int
	}{
		{`{"digest":"` + p.Digest + `"}`, "", 401},
		{`{"digest":"` + p.Digest + `","scope":{}}`, token, 400},
		{`{"digest":"` + p.Digest + `","actor":"forged"}`, token, 400},
		{`{"digest":"` + p.Digest + `","repair_operation_id":"` + uuid.NewString() + `"}`, token, 400},
		{`{"digest":"` + p.Digest + `","expected_sequence":1}`, token, 400},
		{`{"digest":"` + p.Digest + `"}{}`, token, 400},
		{`{"digest":"` + strings.Repeat("0", 64) + `"}`, token, 409},
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(tc.body))
		req.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		server.Config.Handler.ServeHTTP(w, req)
		if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("verification status=%d expected=%d", w.Code, tc.status)
		}
	}
	other := scope
	other.AppID = uuid.NewString()
	if _, err := remote.Verify(ctx, other, p.ID, p.Digest); !errors.Is(err, core.ErrUnavailable) {
		t.Fatal("remote accepted another authenticated scope's report", err)
	}
}

func TestRepairVerificationRejectsInvalidRemoteReports(t *testing.T) {
	scope := core.Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production"}
	id, digest := uuid.NewString(), strings.Repeat("a", 64)
	for _, mutate := range []func(*core.VerificationReport){
		func(r *core.VerificationReport) { r.Scope.AppID = uuid.NewString() },
		func(r *core.VerificationReport) { r.PlanID = uuid.NewString() },
		func(r *core.VerificationReport) { r.PlanDigest = strings.Repeat("b", 64) },
		func(r *core.VerificationReport) { r.Checked, r.Unchanged = 0, 0 },
		func(r *core.VerificationReport) { r.Rows = nil },
		func(r *core.VerificationReport) { r.Available = false },
		func(r *core.VerificationReport) { r.Issues = []string{"journal_purged"} },
	} {
		r := core.VerificationReport{PlanID: id, PlanDigest: digest, Scope: scope, SourceOperationID: uuid.NewString(), RepairOperationID: uuid.NewString(),
			AppliedAt: time.Now(), CheckedAt: time.Now(), Restored: 1, Available: true, Checked: 1, Unchanged: 1, Issues: []string{}, Rows: []core.ValidationRow{}}
		mutate(&r)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(r) }))
		remote, err := NewRemote(server.URL, strings.Repeat("x", 32))
		if err != nil {
			t.Fatal(err)
		}
		_, err = remote.Verify(t.Context(), scope, id, digest)
		server.Close()
		if !errors.Is(err, core.ErrUnavailable) {
			t.Fatal("invalid remote observation accepted", err)
		}
	}
}
