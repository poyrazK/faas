package faas_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestBucketObjectLockClient(t *testing.T) {
	days, years := int32(3), int32(1)
	cfg := faas.ObjectBucketObjectLockConfiguration{Enabled: true, DefaultRetention: &faas.ObjectLockDefaultRetention{Mode: "COMPLIANCE", Days: &days, DefaultEventHold: &faas.ObjectRetentionPeriod{Years: &years}}}
	calls := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.EscapedPath(), "/v1/apps/demo%2Fx/buckets/bucket%2Fx/object-lock") || r.Header.Get("Authorization") != "Bearer token" {
			t.Error(r.URL, r.Header)
		}
		calls = append(calls, r.Method)
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "-capabilities") {
			_, _ = fmt.Fprint(w, `{"bucket_configuration":true,"default_event_hold":true}`)
			return
		}
		if r.Method == "PUT" {
			var in faas.ObjectBucketObjectLockRequest
			if json.NewDecoder(r.Body).Decode(&in) != nil || !in.Configuration.Equal(cfg) {
				t.Error(in)
			}
			w.WriteHeader(202)
		}
		_, _ = fmt.Fprint(w, `{"bucket_id":"bucket","state":"waiting","revision":1,"enabled_required":true,"observed_known":false,"desired_configuration":{"enabled":true,"default_retention":{"mode":"COMPLIANCE","days":3,"default_event_hold":{"years":1}}},"updated_at":"2026-10-04T00:00:00Z"}`)
	}))
	defer srv.Close()
	c, err := faas.NewClient(srv.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	caps, err := c.GetObjectBucketObjectLockCapabilities(context.Background(), "demo/x", "bucket/x")
	if err != nil || !caps.DefaultEventHold {
		t.Fatal(caps, err)
	}
	j, err := c.PutObjectBucketObjectLock(context.Background(), "demo/x", "bucket/x", cfg)
	if err != nil || !j.EnabledRequired || j.ObservedKnown || j.DesiredConfiguration == nil || !j.DesiredConfiguration.Equal(cfg) {
		t.Fatal(j, err)
	}
	if _, err = c.GetObjectBucketObjectLock(context.Background(), "demo/x", "bucket/x"); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(calls) != "[GET PUT GET]" {
		t.Fatal(calls)
	}
}
