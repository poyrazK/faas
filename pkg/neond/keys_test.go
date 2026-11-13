// adr: 20261112160000 — neond mints a key scoped to each project it creates,
// stores it sealed, calls the project's routes with it, falls back to the
// admin key when it has none or Neon refuses it, and revokes it when the
// project is deleted.
package neond

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	neondpb "github.com/onebox-faas/faas/api/proto/onebox/faas/neond/v1"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// memKeys is neon_project_keys in memory.
type memKeys struct {
	mu      sync.Mutex
	rows    map[int64]keyRow
	failPut bool
}

type keyRow struct {
	project string
	sealed  []byte
	revoked bool
	created time.Time
}

func (m *memKeys) LiveProjectKeys(context.Context) ([]LiveKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []LiveKey
	for id, row := range m.rows {
		if !row.revoked {
			out = append(out, LiveKey{KeyID: id, Project: row.project, CreatedAt: row.created})
		}
	}
	return out, nil
}

func (m *memKeys) ReplaceProjectKey(_ context.Context, project string, oldKeyID, newKeyID int64, sealed []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failPut {
		return errors.New("database unavailable")
	}
	old, ok := m.rows[oldKeyID]
	if !ok || old.revoked || old.project != project {
		return ErrKeyNotLive
	}
	old.revoked = true
	m.rows[oldKeyID] = old
	m.rows[newKeyID] = keyRow{project: project, sealed: sealed, created: time.Now()}
	return nil
}

func (m *memKeys) LiveProjectKey(_ context.Context, project string) (StoredKey, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, row := range m.rows {
		if row.project == project && !row.revoked {
			return StoredKey{KeyID: id, Sealed: row.sealed}, true, nil
		}
	}
	return StoredKey{}, false, nil
}

func (m *memKeys) InsertProjectKey(_ context.Context, project string, keyID int64, sealed []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failPut {
		return errors.New("database unavailable")
	}
	for _, row := range m.rows {
		if row.project == project && !row.revoked {
			return errors.New("duplicate live key")
		}
	}
	m.rows[keyID] = keyRow{project: project, sealed: sealed, created: time.Now()}
	return nil
}

func (m *memKeys) LiveProjectKeyIDs(_ context.Context, project string) ([]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []int64
	for id, row := range m.rows {
		if row.project == project && !row.revoked {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (m *memKeys) MarkProjectKeyRevoked(_ context.Context, keyID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row := m.rows[keyID]
	row.revoked = true
	m.rows[keyID] = row
	return nil
}

// keyedNeon is Neon with org keys: it mints and revokes keys, lists and
// creates projects and accepts a project key only on its own project.
type keyedNeon struct {
	mu          sync.Mutex
	next        int64
	keys        map[string]string // key -> project
	projects    map[string]bool
	revoked     []int64
	requests    []string
	failMint    bool
	failRevoke  bool
	unavailable bool
}

func (n *keyedNeon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n.mu.Lock()
	defer n.mu.Unlock()
	auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	n.requests = append(n.requests, r.Method+" "+strings.TrimPrefix(r.URL.Path, "/api/v2")+" "+auth)
	path := strings.TrimPrefix(r.URL.Path, "/api/v2")
	switch {
	case strings.HasPrefix(path, "/organizations/org-1/api_keys"):
		if auth != "admin-key" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost {
			if n.failMint {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			var body struct {
				Name    string `json:"key_name"`
				Project string `json:"project_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			n.next++
			key := "napi_project_" + strconv.FormatInt(n.next, 10)
			n.keys[key] = body.Project
			_ = json.NewEncoder(w).Encode(map[string]any{"id": n.next, "key": key, "name": body.Name})
			return
		}
		if n.failRevoke {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		id, _ := strconv.ParseInt(path[strings.LastIndex(path, "/")+1:], 10, 64)
		n.revoked = append(n.revoked, id)
		for key := range n.keys {
			if strings.HasSuffix(key, "_"+strconv.FormatInt(id, 10)) {
				delete(n.keys, key)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "revoked": true})
	case r.Method == http.MethodPost && path == "/projects":
		n.projects["p-new"] = true
		_ = json.NewEncoder(w).Encode(map[string]any{"project": map[string]any{"id": "p-new"}})
	case r.Method == http.MethodGet && path == "/projects":
		if auth != "admin-key" || r.URL.Query().Get("org_id") != "org-1" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		list := []map[string]any{}
		for project := range n.projects {
			list = append(list, map[string]any{"id": project})
		}
		reply := map[string]any{"projects": list, "pagination": map[string]any{}}
		if n.unavailable {
			reply["unavailable_project_ids"] = []string{"p-hidden"}
		}
		_ = json.NewEncoder(w).Encode(reply)
	case r.Method == http.MethodDelete && strings.Count(path, "/") == 2:
		delete(n.projects, strings.TrimPrefix(path, "/projects/"))
		_, _ = io.WriteString(w, `{}`)
	default:
		project := strings.Split(strings.TrimPrefix(path, "/projects/"), "/")[0]
		if auth != "admin-key" && n.keys[auth] != project {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}
}

func (n *keyedNeon) last() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.requests[len(n.requests)-1]
}

func newKeyedServer(t *testing.T) (*Server, *keyedNeon, *memKeys, *age.X25519Identity, *prometheus.Registry) {
	t.Helper()
	neon := &keyedNeon{keys: map[string]string{}, projects: map[string]bool{}}
	ts := httptest.NewServer(neon)
	t.Cleanup(ts.Close)
	base, _ := url.Parse(ts.URL + "/api/v2")
	reg := prometheus.NewRegistry()
	server, err := NewServer(base, ts.Client(), "admin-key", fakeLive{}, 100, reg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	store := &memKeys{rows: map[int64]keyRow{}}
	if err := server.UseProjectKeys(ProjectKeys{Org: "org-1", Identities: []*age.X25519Identity{identity}, Store: store}); err != nil {
		t.Fatal(err)
	}
	return server, neon, store, identity, reg
}

func forward(t *testing.T, s *Server, method, path string) int32 {
	t.Helper()
	reply, err := s.Forward(context.Background(), &neondpb.ForwardRequest{Method: method, Path: path})
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return reply.GetStatus()
}

func TestProjectKeyLifecycle(t *testing.T) {
	server, neon, store, identity, reg := newKeyedServer(t)

	// Create: the key is minted with the admin key, sealed and stored
	// before the reply.
	forward(t, server, http.MethodPost, "/projects")
	stored, ok, _ := store.LiveProjectKey(context.Background(), "p-new")
	if !ok {
		t.Fatalf("no key stored; Neon saw %v", neon.requests)
	}
	namespace, plain, err := secretbox.OpenBytes(identity, stored.Sealed)
	if err != nil || namespace != sealNamespace || string(plain) != "napi_project_1" {
		t.Fatalf("stored key opens to %q %q %v", namespace, plain, err)
	}

	// Project routes go out with the project key; admin routes never do.
	if got := forward(t, server, http.MethodGet, "/projects/p-new/branches"); got != http.StatusOK || neon.last() != "GET /projects/p-new/branches napi_project_1" {
		t.Fatalf("project route = %d, Neon saw %q", got, neon.last())
	}
	forward(t, server, http.MethodGet, "/projects")
	if neon.last() != "GET /projects admin-key" {
		t.Fatalf("admin route went out as %q", neon.last())
	}

	// A project without a key uses the admin key.
	forward(t, server, http.MethodGet, "/projects/p-old")
	if neon.last() != "GET /projects/p-old admin-key" {
		t.Fatalf("keyless project went out as %q", neon.last())
	}

	// Delete: the project's key is revoked at Neon and marked revoked.
	forward(t, server, http.MethodDelete, "/projects/p-new")
	if len(neon.revoked) != 1 || neon.revoked[0] != 1 {
		t.Fatalf("revoked = %v", neon.revoked)
	}
	if _, ok, _ := store.LiveProjectKey(context.Background(), "p-new"); ok {
		t.Fatal("key still live after the project was deleted")
	}
	if got := testutil.ToFloat64(server.keyOps.WithLabelValues("mint", "ok")); got != 1 {
		t.Fatalf("mint ok = %v", got)
	}
	if got := testutil.ToFloat64(server.requests.WithLabelValues("read project", "admin_fallback", "forwarded")); got != 1 {
		t.Fatalf("admin_fallback reads = %v", got)
	}
	_ = reg
}

func TestRefusedProjectKeyFallsBackToAdmin(t *testing.T) {
	server, neon, _, _, _ := newKeyedServer(t)
	forward(t, server, http.MethodPost, "/projects")
	// Revoke the key behind neond's back.
	neon.mu.Lock()
	neon.keys = map[string]string{}
	neon.mu.Unlock()
	if got := forward(t, server, http.MethodGet, "/projects/p-new"); got != http.StatusOK {
		t.Fatalf("status = %d; want the admin retry to succeed", got)
	}
	if neon.last() != "GET /projects/p-new admin-key" {
		t.Fatalf("retry went out as %q", neon.last())
	}
}

func TestMintFailureLeavesProjectOnAdminKey(t *testing.T) {
	server, neon, store, _, _ := newKeyedServer(t)
	neon.failMint = true
	if got := forward(t, server, http.MethodPost, "/projects"); got != http.StatusOK {
		t.Fatalf("create = %d; a failed mint must not fail the create", got)
	}
	if _, ok, _ := store.LiveProjectKey(context.Background(), "p-new"); ok {
		t.Fatal("key stored although minting failed")
	}
	if got := testutil.ToFloat64(server.keyOps.WithLabelValues("mint", "error")); got != 1 {
		t.Fatalf("mint error = %v", got)
	}
}

// A key minted but not stored is revoked, so no live key exists that neond
// does not hold.
func TestUnstoredKeyIsRevoked(t *testing.T) {
	server, neon, store, _, _ := newKeyedServer(t)
	store.failPut = true
	forward(t, server, http.MethodPost, "/projects")
	if len(neon.revoked) != 1 || len(neon.keys) != 0 {
		t.Fatalf("revoked = %v, live keys = %v", neon.revoked, neon.keys)
	}
}

func TestUseProjectKeysValidates(t *testing.T) {
	server, _, store, identity, _ := newKeyedServer(t)
	ids := []*age.X25519Identity{identity}
	for _, keys := range []ProjectKeys{
		{Identities: ids, Store: store},
		{Org: "org/../x", Identities: ids, Store: store},
		{Org: "org-1", Store: store},
		{Org: "org-1", Identities: []*age.X25519Identity{nil}, Store: store},
		{Org: "org-1", Identities: ids},
	} {
		if err := server.UseProjectKeys(keys); err == nil {
			t.Errorf("UseProjectKeys(%+v) = nil", keys)
		}
	}
}
