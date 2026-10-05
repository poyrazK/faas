// adr: 426 — object-storage canaries report bounded read-access evidence.
package bindingprobe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func objectStorageTestEnv(endpoint string) map[string]string {
	return map[string]string{
		"ASSETS_ENDPOINT": endpoint, "ASSETS_REGION": "eu-test-1", "ASSETS_BUCKET": "private-bucket",
		"ASSETS_ACCESS_KEY_ID": "private-access-key", "ASSETS_SECRET_ACCESS_KEY": "private-secret-key", "ASSETS_ADDRESSING_STYLE": "path",
	}
}

func TestObjectStorageSignedBoundedReadAndEmptyBucket(t *testing.T) {
	for _, contents := range []string{"", "<Contents><Key>private-object-name</Key><Size>5</Size></Contents>"} {
		t.Run(fmt.Sprintf("objects=%t", contents != ""), func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != http.MethodGet || r.URL.Path != "/private-bucket" || r.URL.Query().Get("list-type") != "2" ||
					r.URL.Query().Get("max-keys") != "1" || r.URL.Query().Has("continuation-token") ||
					!strings.Contains(r.Header.Get("Authorization"), "Credential=private-access-key/") || !strings.Contains(r.Header.Get("Authorization"), "/eu-test-1/s3/aws4_request") {
					t.Errorf("unexpected probe operation: %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/xml")
				_, _ = fmt.Fprintf(w, `<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Name>private-bucket</Name><IsTruncated>true</IsTruncated>%s</ListBucketResult>`, contents)
			}))
			defer srv.Close()
			env := objectStorageTestEnv(srv.URL)
			report := ObjectStorage(context.Background(), "ASSETS", func(key string) string { return env[key] })
			if !report.Passed() || requests != 1 || report.Prefix != "ASSETS" {
				t.Fatalf("report=%+v requests=%d", report, requests)
			}
			raw, _ := json.Marshal(report)
			for _, private := range []string{"private-bucket", "private-access-key", "private-secret-key", "private-object-name", srv.URL} {
				if strings.Contains(string(raw), private) {
					t.Fatalf("report leaked binding data: %s", raw)
				}
			}
		})
	}
}

func TestObjectStorageRejectsIncompleteOrInvalidEnvironmentBeforeRequests(t *testing.T) {
	for _, tc := range []struct{ key, value, stage string }{
		{"ASSETS_SECRET_ACCESS_KEY", "", "environment"}, {"ASSETS_ADDRESSING_STYLE", "", "environment"},
		{"ASSETS_ENDPOINT", "https://user:private-secret-key@example.test", "configuration"},
		{"ASSETS_ENDPOINT", "https://example.test?token=private-secret-key", "configuration"},
		{"ASSETS_ENDPOINT", "file:///private", "configuration"}, {"ASSETS_ENDPOINT", "https://", "configuration"},
		{"ASSETS_BUCKET", "bucket/escape", "configuration"}, {"ASSETS_ADDRESSING_STYLE", "virtual", "configuration"},
	} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++ }))
			defer srv.Close()
			env := objectStorageTestEnv(srv.URL)
			env[tc.key] = tc.value
			report := ObjectStorage(context.Background(), "ASSETS", func(key string) string { return env[key] })
			stage := report.Configuration.Status
			if tc.stage == "environment" {
				stage = report.Environment.Status
			}
			if report.Passed() || stage != "failed" || requests != 0 {
				t.Fatalf("invalid configuration reached network: %+v requests=%d", report, requests)
			}
		})
	}
	for _, prefix := range []string{"", "lowercase", "ASSETS-INVALID", "A" + strings.Repeat("B", 48)} {
		report := ObjectStorage(context.Background(), prefix, func(string) string { t.Fatal("invalid prefix read environment"); return "" })
		if report.Environment.Status != "failed" || report.Prefix != "" {
			t.Fatalf("invalid prefix report=%+v", report)
		}
	}
}

func TestObjectStorageSafeAuthorizationAndProviderFailuresWithoutRetries(t *testing.T) {
	for _, tc := range []struct {
		status       int
		code         string
		auth, access string
	}{
		{401, "InvalidAccessKeyId", "failed", "not_checked"}, {403, "AccessDenied", "failed", "not_checked"},
		{404, "NoSuchBucket", "not_checked", "failed"}, {503, "ServiceUnavailable", "not_checked", "failed"},
		{200, "malformed", "not_checked", "failed"}, {200, "oversized", "not_checked", "failed"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.WriteHeader(tc.status)
				if tc.code == "malformed" {
					_, _ = w.Write([]byte("not-xml private-secret-key"))
					return
				}
				if tc.code == "oversized" {
					_, _ = fmt.Fprintf(w, "<ListBucketResult><Contents><Key>%s</Key></Contents></ListBucketResult>", strings.Repeat("private-secret-key", 5000))
					return
				}
				_, _ = fmt.Fprintf(w, "<Error><Code>%s</Code><Message>private-secret-key private-provider-error</Message></Error>", tc.code)
			}))
			defer srv.Close()
			env := objectStorageTestEnv(srv.URL)
			report := ObjectStorage(context.Background(), "ASSETS", func(key string) string { return env[key] })
			if report.Passed() || report.Connection.Status != "passed" || report.Authorization.Status != tc.auth || report.BucketAccess.Status != tc.access || requests != 1 {
				t.Fatalf("report=%+v requests=%d", report, requests)
			}
			raw, _ := json.Marshal(report)
			if strings.Contains(string(raw), "private-") || strings.Contains(string(raw), srv.URL) {
				t.Fatalf("raw provider diagnostic leaked: %s", raw)
			}
		})
	}
}

func TestObjectStorageDoesNotForwardCredentialsOnRedirect(t *testing.T) {
	forwarded := 0
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded++ }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	env := objectStorageTestEnv(srv.URL)
	report := ObjectStorage(context.Background(), "ASSETS", func(key string) string { return env[key] })
	if report.Passed() || forwarded != 0 || report.BucketAccess.Status != "failed" {
		t.Fatalf("redirect report=%+v forwarded=%d", report, forwarded)
	}
}

func TestObjectStorageTLSFailureAndCancelledDeadline(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("untrusted TLS request reached handler") }))
	defer srv.Close()
	env := objectStorageTestEnv(srv.URL)
	report := ObjectStorage(context.Background(), "ASSETS", func(key string) string { return env[key] })
	if report.Connection.Status != "failed" || report.Authorization.Status != "not_checked" {
		t.Fatalf("TLS failure=%+v", report)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	report = ObjectStorage(ctx, "ASSETS", func(key string) string { return env[key] })
	if report.Connection.Status != "failed" || time.Since(started) > time.Second {
		t.Fatalf("cancelled probe=%+v", report)
	}
}
