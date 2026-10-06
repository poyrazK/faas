package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestVersionProtectionClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
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
	c, err := faas.NewClient(s.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := c.GetObjectVersionRetention(ctx, "demo", "bucket", key, version); err != nil || !out.Retention.Empty() {
		t.Fatal(out, err)
	}
	if out, err := c.PutObjectVersionRetention(ctx, "demo", "bucket", key, version, faas.ObjectVersionRetentionRequest{ID: id}); err != nil || out.State != "waiting" {
		t.Fatal(out, err)
	}
	if out, err := c.GetObjectVersionLegalHold(ctx, "demo", "bucket", key, version); err != nil || out.LegalHold.Status != "ON" {
		t.Fatal(out, err)
	}
	if out, err := c.PutObjectVersionLegalHold(ctx, "demo", "bucket", key, version, faas.ObjectVersionLegalHoldRequest{ID: id, LegalHold: faas.ObjectVersionLegalHold{Status: "OFF"}}); err != nil || out.ID != id {
		t.Fatal(out, err)
	}
	if out, err := c.GetObjectVersionProtection(ctx, "demo", "bucket", id); err != nil || out.State != "ready" {
		t.Fatal(out, err)
	}
	if calls != 5 {
		t.Fatal(calls)
	}
}

// adr: 608
func TestEventHoldProtectionClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	day, year := int32(30), int32(1)
	for _, policy := range []faas.ObjectVersionRetention{
		{Mode: "COMPLIANCE", EventHold: "ON", EventHoldDuration: &faas.ObjectRetentionPeriod{Days: &day}},
		{Mode: "GOVERNANCE", EventHold: "ON", EventHoldDuration: &faas.ObjectRetentionPeriod{Years: &year}},
		{Mode: "COMPLIANCE", EventHold: "OFF"},
	} {
		t.Run(policy.Mode+policy.EventHold, func(t *testing.T) {
			in := faas.ObjectVersionRetentionRequest{ID: "00000000-0000-4000-8000-000000000001", Retention: policy}
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var got faas.ObjectVersionRetentionRequest
				if r.Method != "PUT" || r.URL.Query().Get("version_id") != "null" || r.URL.Query().Get("key") != "目录/+ %" || json.NewDecoder(r.Body).Decode(&got) != nil {
					t.Error("invalid request", r.URL)
				}
				want, _ := json.Marshal(in)
				body, _ := json.Marshal(got)
				if string(want) != string(body) {
					t.Error("changed event intent", string(body))
				}
				w.WriteHeader(202)
				_, _ = w.Write([]byte(`{"id":"` + in.ID + `","state":"waiting"}`))
			}))
			defer s.Close()
			c, e := faas.NewClient(s.URL, "token")
			if e != nil {
				t.Fatal(e)
			}
			if out, e := c.PutObjectVersionRetention(ctx, "demo", "bucket", "目录/+ %", "null", in); e != nil || out.ID != in.ID {
				t.Fatal(out, e)
			}
		})
	}
}
