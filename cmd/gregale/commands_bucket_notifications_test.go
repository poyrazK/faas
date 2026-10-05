package main

import (
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBucketNotificationsCLI(t *testing.T) {
	in, err := decodeBucketNotificationsFile(strings.NewReader(`{"rules":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{}`, `{"rules":null}`, `{"rules":[],"unknown":true}`, `{"rules":[]} {}`, `{"rules":[]}` + strings.Repeat(" ", int(api.MaxObjectNotificationBodyBytes))} {
		if _, e := decodeBucketNotificationsFile(strings.NewReader(body)); e == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
	for _, action := range []string{"get", "set", "clear"} {
		t.Run(action, func(t *testing.T) {
			method := map[string]string{"get": "GET", "set": "PUT", "clear": "DELETE"}[action]
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != method || r.URL.Path != "/v1/apps/demo/buckets/bucket/notifications" || r.Header.Get("Authorization") != "Bearer token" {
					t.Error(r.Method, r.URL)
				}
				_, _ = fmt.Fprint(w, `{"bucket_id":"bucket","revision":2,"rules":[]}`)
			}))
			defer srv.Close()
			if p, e := runBucketNotifications(t.Context(), api.NewClient(srv.URL, "token"), []string{action, "demo", "bucket"}, in); e != nil || p.Revision != 2 {
				t.Fatal(p, e)
			}
		})
	}
}
