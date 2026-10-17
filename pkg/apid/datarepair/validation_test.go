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

func TestSelectiveRepairValidationReadOnlyHTTP(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenDatabase(t)
	if _, err := pool.Exec(ctx, core.Schema()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE products(id int PRIMARY KEY,category text NOT NULL);
SELECT gregale_repair.enroll('products',ARRAY['category']);INSERT INTO products VALUES(1,'old'),(2,'old')`); err != nil {
		t.Fatal(err)
	}
	scope := core.Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production"}
	source := uuid.NewString()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.Capture(ctx, tx, scope, source, "import", "products", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE products SET category='bad'`)
		return err
	}); err != nil {
		_ = tx.Rollback(ctx)
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
	token := strings.Repeat("v", 32)
	server := httptest.NewServer((&Handler{Engine: engine, Authenticate: Bearer(token, Principal{Scope: scope, Actor: "reader"})}).Routes())
	defer server.Close()
	remote, err := NewRemote(server.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	r, err := remote.Validate(ctx, scope, p.ID, p.Digest)
	if err != nil || !r.Matches || r.Checked != 2 {
		t.Fatal("a read-only principal could not validate", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE products SET category='human' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	r, err = remote.Validate(ctx, scope, p.ID, p.Digest)
	if err != nil || r.Matches || r.Changed != 1 || r.Rows[0].KeyJSON != `{"id": 1}` {
		t.Fatalf("HTTP drift report: %+v %v", r, err)
	}
	after, err := engine.Get(ctx, scope, p.ID)
	beforeJSON, _ := json.Marshal(p)
	afterJSON, _ := json.Marshal(after)
	if err != nil || string(beforeJSON) != string(afterJSON) || after.RehearsedAt != nil || after.ApprovedAt != nil {
		t.Fatal("HTTP validation mutated the saved plan or approval", err)
	}
	path := "/v1/data-repair/plans/" + p.ID + "/validate"
	for _, test := range []struct {
		body, token string
		status      int
	}{
		{`{"digest":"` + p.Digest + `"}`, "", 401},
		{`{"digest":"` + p.Digest + `","scope":{}}`, token, 400},
		{`{"digest":"` + p.Digest + `","actor":"forged"}`, token, 400},
		{`{"digest":"` + p.Digest + `","expected_sequence":1}`, token, 400},
		{`{"digest":"` + p.Digest + `"} {}`, token, 400},
		{`{"digest":"` + strings.Repeat("0", 64) + `"}`, token, 409},
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(test.body))
		req.Header.Set("Authorization", "Bearer "+test.token)
		response := httptest.NewRecorder()
		server.Config.Handler.ServeHTTP(response, req)
		if response.Code != test.status || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("validation request status %d, expected %d", response.Code, test.status)
		}
	}
	other := scope
	other.AppID = uuid.NewString()
	if _, err := remote.Validate(ctx, other, p.ID, p.Digest); !errors.Is(err, core.ErrUnavailable) {
		t.Fatal("remote returned another authenticated scope's report", err)
	}
}

func TestSelectiveRepairValidationRejectsInvalidRemoteReports(t *testing.T) {
	scope := core.Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production"}
	id, digest := uuid.NewString(), strings.Repeat("a", 64)
	for _, mutate := range []func(*core.ValidationReport){
		func(r *core.ValidationReport) { r.Scope.AppID = uuid.NewString() },
		func(r *core.ValidationReport) { r.PlanID = uuid.NewString() },
		func(r *core.ValidationReport) { r.PlanDigest = strings.Repeat("b", 64) },
		func(r *core.ValidationReport) { r.Checked, r.Unchanged = 0, 0 },
		func(r *core.ValidationReport) { r.Issues = []string{"candidate_drift"} },
		func(r *core.ValidationReport) { r.Rows = nil },
	} {
		r := core.ValidationReport{PlanID: id, PlanDigest: digest, Scope: scope, CheckedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(time.Minute).UTC(), Matches: true, Candidates: 1, Checked: 1, Unchanged: 1, Issues: []string{}, Rows: []core.ValidationRow{}}
		mutate(&r)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(r) }))
		client, err := NewRemote(server.URL, strings.Repeat("x", 32))
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.Validate(t.Context(), scope, id, digest)
		server.Close()
		if !errors.Is(err, core.ErrUnavailable) {
			t.Fatal("invalid or mismatched validation report was accepted", err)
		}
	}
}
