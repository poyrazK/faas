package datarepair

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestRepairReplacementAuthenticatedExecutorJourney(t *testing.T) {
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
	if err := core.Capture(ctx, tx, scope, source, "bad-import", "products", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE products SET category='bad'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	engine := &core.Engine{Pool: pool, Rehearser: testRunner{}}
	parent, err := engine.Plan(ctx, scope, source)
	if err != nil {
		t.Fatal(err)
	}
	parent, err = engine.Rehearse(ctx, scope, parent.ID, parent.Digest)
	if err != nil {
		t.Fatal(err)
	}
	parent, err = engine.Approve(ctx, scope, parent.ID, parent.Digest, "original-reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE products SET category='human' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	token, reader := strings.Repeat("x", 64), strings.Repeat("r", 64)
	auth := func(r *http.Request) (Principal, error) {
		if r.Header.Get("Authorization") == "Bearer "+reader {
			return Bearer(reader, Principal{Scope: scope, Actor: "reader"})(r)
		}
		return Bearer(token, Principal{Scope: scope, Actor: "manager", CanApply: true, CanApprove: true})(r)
	}
	server := httptest.NewServer((&Handler{Engine: engine, Authenticate: auth}).Routes())
	defer server.Close()
	id := uuid.NewString()
	path := server.URL + "/v1/data-repair/plans/" + parent.ID + "/replace"
	for _, tc := range []struct {
		body, token string
		status      int
	}{
		{`{"digest":"` + parent.Digest + `","plan_id":"` + id + `"}`, "", 401},
		{`{"digest":"` + parent.Digest + `","plan_id":"` + id + `"}`, reader, 403},
		{`{"digest":"` + parent.Digest + `"}`, token, 400},
		{`{"digest":"` + parent.Digest + `","plan_id":"` + id + `","actor":"forged"}`, token, 400},
		{`{"digest":"` + parent.Digest + `","plan_id":"` + id + `","scope":{}}`, token, 400},
		{`{"digest":"` + parent.Digest + `","plan_id":"` + id + `"}{}`, token, 400},
		{`{"digest":"` + strings.Repeat("0", 64) + `","plan_id":"` + id + `"}`, token, 409},
	} {
		req, _ := http.NewRequestWithContext(ctx, "POST", path, strings.NewReader(tc.body))
		req.Header.Set("Authorization", "Bearer "+tc.token)
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != tc.status || resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("replacement request status=%d want=%d", resp.StatusCode, tc.status)
		}
	}
	remote, err := NewRemote(server.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		child, err := remote.Replace(ctx, scope, parent.ID, parent.Digest, id)
		if err != nil || child.Candidates != 1 || child.Conflicts != 1 || !replacementMatches(child, parent.ID, parent.Digest, id) || child.ApprovedAt != nil {
			t.Fatalf("authenticated replacement: %+v %v", child, err)
		}
	}
	other := scope
	other.Environment = "staging"
	if _, err := remote.Replace(ctx, other, parent.ID, parent.Digest, id); !errors.Is(err, core.ErrUnavailable) {
		t.Fatal("remote replacement accepted a different scope", err)
	}
}

func TestRepairReplacementRemoteRejectsMismatchedLineage(t *testing.T) {
	scope := core.Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production"}
	parent, child, digest := uuid.NewString(), uuid.NewString(), strings.Repeat("a", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(core.Plan{ID: child, Scope: scope, Replacement: &core.Replacement{
			ParentPlanID: uuid.NewString(), ParentPlanDigest: digest, PreviousCandidates: 1, RetainedCandidates: 1}})
	}))
	defer server.Close()
	remote, err := NewRemote(server.URL, strings.Repeat("x", 64))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := remote.Replace(context.Background(), scope, parent, digest, child); !errors.Is(err, core.ErrUnavailable) {
		t.Fatal("remote accepted mismatched lineage", err)
	}
}
