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
	"github.com/onebox-faas/faas/pkg/api"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestRepairHistoryAuthenticatedReader(t *testing.T) {
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
	token := strings.Repeat("r", 32)
	handler := (&Handler{Engine: engine, Authenticate: Bearer(token, Principal{Scope: scope, Actor: "reader"})}).Routes()
	server := httptest.NewServer(handler)
	defer server.Close()
	remote, err := NewRemote(server.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	page, err := remote.History(ctx, scope, source, core.HistoryQuery{})
	if err != nil || !page.Valid(api.DataRepairSourcePageSize) || len(page.Items) != 1 || page.Items[0].ID != p.ID || page.Items[0].ApprovedAt == nil || page.Items[0].ApplyResolution != nil || page.Items[0].AppliedAt != nil || page.Items[0].Execution != nil {
		t.Fatal("reader could not discover an approved unresolved plan", page, err)
	}
	path := "/v1/data-repair/sources/" + source + "/history"
	for _, tc := range []struct {
		path, token string
		status      int
	}{
		{path, "", 401}, {path, token, 200},
		{path + "?scope=forged", token, 400}, {path + "?release=bad-import", token, 400},
		{path + "?environment=staging", token, 400}, {path + "?limit=1&limit=2", token, 400},
		{path + "?cursor=invalid", token, 400}, {path + "?limit=0", token, 400},
		{"/v1/data-repair/sources/" + uuid.NewString() + "/history", token, 404},
	} {
		r := httptest.NewRequest("GET", tc.path, nil)
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), `"category"`) {
			t.Fatal("history permission/filter/payload guard", tc, w.Code, w.Body.String())
		}
	}
	other := scope
	other.PlatformTenantID = uuid.NewString()
	if _, err := remote.History(ctx, other, source, core.HistoryQuery{}); !errors.Is(err, core.ErrUnavailable) {
		t.Fatal("remote history accepted a different authenticated scope", err)
	}
}

func TestRepairHistoryRemoteRejectsUntrustedMetadata(t *testing.T) {
	scope := core.Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production"}
	source := uuid.NewString()
	redirects := 0
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirects++ }))
	defer destination.Close()
	for _, mode := range []string{"scope", "source", "counts", "receipt", "execution", "oversized", "redirect", "not_found", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("x", 32) || r.Method != "GET" || r.URL.Path != "/v1/data-repair/sources/"+source+"/history" || r.ContentLength > 0 {
					t.Error("history changed method, identity or credentials")
				}
				p := core.HistoryPage{Scope: scope, SourceOperationID: source, CheckedAt: time.Now(), Items: []core.HistoryItem{{ID: uuid.NewString(), Digest: strings.Repeat("a", 64), CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour), Candidates: 1}}}
				switch mode {
				case "scope":
					p.Scope.AppID = uuid.NewString()
				case "source":
					p.SourceOperationID = uuid.NewString()
				case "counts":
					p.Items[0].Candidates = api.DataRepairMaxRows + 1
				case "receipt":
					now := time.Now()
					p.Items[0].AppliedAt = &now
				case "execution":
					p.Items[0].Execution = &core.HistoryExecution{Availability: "not_retained"}
				case "oversized":
					_, _ = w.Write([]byte(strings.Repeat("x", api.DataRepairSourceMaxResponseBytes+1)))
					return
				case "redirect":
					http.Redirect(w, r, destination.URL, 307)
					return
				case "not_found":
					w.WriteHeader(404)
					return
				case "invalid":
					w.WriteHeader(400)
					return
				}
				_ = json.NewEncoder(w).Encode(p)
			}))
			defer server.Close()
			remote, err := NewRemote(server.URL, strings.Repeat("x", 32))
			if err != nil {
				t.Fatal(err)
			}
			_, err = remote.History(t.Context(), scope, source, core.HistoryQuery{Limit: 1})
			want := core.ErrUnavailable
			if mode == "not_found" {
				want = core.ErrNotFound
			} else if mode == "invalid" {
				want = core.ErrInvalid
			}
			if !errors.Is(err, want) || redirects != 0 {
				t.Fatal("untrusted history response accepted", mode, err, redirects)
			}
		})
	}
	b := Binding{Scope: scope, Identity: "new-binding", Executor: &core.Engine{}}
	managed := &Managed{}
	if _, err := managed.historyPage(t.Context(), b, source, core.HistoryQuery{Cursor: core.EncodeSourceCursor(boundSourceCursor{Binding: "old-binding", Cursor: "prior-page"})}); !errors.Is(err, core.ErrInvalid) {
		t.Fatal("history continuation survived executor replacement", err)
	}
}
