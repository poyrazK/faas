package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVersionProtectionClient(t *testing.T) {
	key := "目录/+ %"
	id := "00000000-0000-4000-8000-000000000001"
	version := "00000000-0000-4000-8000-000000000002"
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing auth")
		}
		if strings.Contains(r.URL.Path, "protection-operations") {
			if r.URL.RawQuery != "" || !strings.HasSuffix(r.URL.Path, "/"+id) {
				t.Error(r.URL)
			}
			_, _ = w.Write([]byte(`{"id":"` + id + `","state":"ready"}`))
			return
		}
		if r.URL.Query().Get("key") != key || r.URL.Query().Get("version_id") != version {
			t.Error("selector changed", r.URL)
		}
		if r.Method == http.MethodPut {
			var in map[string]any
			if json.NewDecoder(r.Body).Decode(&in) != nil || in["id"] != id {
				t.Error("missing stable identity", in)
			}
			w.WriteHeader(202)
			_, _ = w.Write([]byte(`{"id":"` + id + `","state":"waiting"}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/retention") {
			_, _ = w.Write([]byte(`{"version_id":"` + version + `","retention":{}}`))
		} else {
			_, _ = w.Write([]byte(`{"version_id":"` + version + `","legal_hold":{"status":"ON"}}`))
		}
	}))
	defer s.Close()
	c := NewClient(s.URL, "token")
	if out, err := c.GetObjectVersionRetention(t.Context(), "demo", "bucket", key, version); err != nil || !out.Retention.Empty() {
		t.Fatal(out, err)
	}
	if out, err := c.PutObjectVersionRetention(t.Context(), "demo", "bucket", key, version, ObjectVersionRetentionRequest{ID: id}); err != nil || out.State != "waiting" {
		t.Fatal(out, err)
	}
	if out, err := c.GetObjectVersionLegalHold(t.Context(), "demo", "bucket", key, version); err != nil || out.LegalHold.Status != "ON" {
		t.Fatal(out, err)
	}
	if out, err := c.PutObjectVersionLegalHold(t.Context(), "demo", "bucket", key, version, ObjectVersionLegalHoldRequest{ID: id, LegalHold: ObjectVersionLegalHold{Status: "OFF"}}); err != nil || out.ID != id {
		t.Fatal(out, err)
	}
	if out, err := c.GetObjectVersionProtection(t.Context(), "demo", "bucket", id); err != nil || out.State != "ready" {
		t.Fatal(out, err)
	}
	if calls != 5 {
		t.Fatal(calls)
	}
}
