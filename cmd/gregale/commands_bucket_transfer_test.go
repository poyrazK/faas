package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type transferFixture struct {
	url       string
	calls     int
	multipart bool
	session   api.ObjectMultipartUpload
	parts     []api.ObjectMultipartCompletedPart
	request   api.ObjectSignRequest
}

func (c *transferFixture) BaseURL() string { return "https://transfer.example.test" }
func (c *transferFixture) GetObjectMultipartUpload(context.Context, string, string, string) (api.ObjectMultipartUpload, error) {
	return c.session, nil
}
func (c *transferFixture) ListObjectMultipartParts(context.Context, string, string, string, int, int) (api.ObjectMultipartPartList, error) {
	return api.ObjectMultipartPartList{Items: []api.ObjectMultipartPart{}}, nil
}

func (c *transferFixture) ListObjectBuckets(context.Context, string) (api.ObjectBucketList, error) {
	return api.ObjectBucketList{Enabled: true, MaxSinglePutBytes: 3, MaxUploadBytes: 20}, nil
}
func (c *transferFixture) SignBucketObject(_ context.Context, _, _ string, r api.ObjectSignRequest) (api.ObjectSignedRequest, error) {
	c.calls++
	c.request = r
	return api.ObjectSignedRequest{URL: c.url, Method: r.Method, ExpiresAt: time.Now().Add(time.Minute), UploadID: "receipt"}, nil
}
func (c *transferFixture) CreateObjectMultipartUpload(_ context.Context, _, _ string, r api.CreateObjectMultipartUploadRequest) (api.ObjectMultipartUpload, error) {
	c.calls++
	c.multipart = true
	return c.session, nil
}
func (c *transferFixture) SignObjectMultipartPart(_ context.Context, _, _, _ string, part int, _ api.ObjectMultipartPartSignRequest) (api.ObjectSignedRequest, error) {
	c.calls++
	return api.ObjectSignedRequest{URL: c.url, Method: "PUT", ExpiresAt: time.Now().Add(time.Minute)}, nil
}
func (c *transferFixture) CompleteObjectMultipartUpload(_ context.Context, _, _, _ string, r api.CompleteObjectMultipartUploadRequest) (api.ObjectMultipartUpload, error) {
	c.calls++
	c.parts = r.Parts
	out := c.session
	out.State = "completed"
	out.ETag = `"whole"`
	return out, nil
}

// adr: 628
func TestBucketFileTransferStreamsSingleAndMultipart(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	for _, payload := range []string{"", "abc", "abcdefg"} {
		t.Run(payload, func(t *testing.T) {
			version := uuid.NewString()
			var received bytes.Buffer
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("API authentication leaked to transfer")
				}
				_, _ = io.Copy(&received, r.Body)
				w.Header().Set("ETag", `"part"`)
				w.Header().Set("X-Amz-Version-Id", version)
			}))
			defer server.Close()
			file := filepath.Join(t.TempDir(), "input")
			if err := os.WriteFile(file, []byte(payload), 0600); err != nil {
				t.Fatal(err)
			}
			c := &transferFixture{url: server.URL, session: api.ObjectMultipartUpload{ID: uuid.NewString(), Key: "目录 /+%.txt", SizeBytes: int64(len(payload)), PartSizeBytes: 3, PartCount: 3, VersionID: version, State: "active", ContentType: "text/plain", ExpiresAt: time.Now().Add(time.Hour)}}
			result, err := runBucketTransfer(t.Context(), c, bucketTransferOptions{action: "upload", app: "demo", bucket: uuid.NewString(), key: "目录 /+%.txt", path: file, contentType: "text/plain"})
			if err != nil || result.Status != "completed" || result.VersionID != version || result.Bytes != int64(len(payload)) || received.String() != payload || c.multipart != (len(payload) > 3) {
				t.Fatal(result, err, received.String())
			}
			if c.multipart && (len(c.parts) != 3 || c.parts[2].PartNumber != 3) {
				t.Fatal(c.parts)
			}
		})
	}
}

// adr: 628
func TestBucketDownloadPublishesOnlyCompleteData(t *testing.T) {
	for _, tc := range []struct {
		name          string
		status        int
		body          string
		length        string
		force, exists bool
		success       bool
	}{
		{"new", 200, "complete", "8", false, false, true},
		{"existing", 200, "complete", "8", false, true, false},
		{"replace", 200, "complete", "8", true, true, true},
		{"short response", 200, "short", "8", true, true, false},
		{"unexpected partial", 206, "short", "5", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", tc.length)
				w.Header().Set("ETag", `"etag"`)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			file := filepath.Join(t.TempDir(), "destination")
			if tc.exists {
				if err := os.WriteFile(file, []byte("original"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			c := &transferFixture{url: server.URL}
			_, err := runBucketTransfer(t.Context(), c, bucketTransferOptions{action: "download", app: "demo", bucket: uuid.NewString(), key: "key", path: file, force: tc.force})
			if (err == nil) != tc.success {
				t.Fatal(err)
			}
			content, _ := os.ReadFile(file)
			if tc.success && string(content) != tc.body || !tc.success && tc.exists && string(content) != "original" {
				t.Fatal("destination corrupted", string(content))
			}
			entries, _ := os.ReadDir(filepath.Dir(file))
			if len(entries) != 1 {
				t.Fatal("temporary files leaked", entries)
			}
		})
	}
}

// adr: 628
func TestBucketCapabilityRefusesRedirectAndSecrets(t *testing.T) {
	visits := 0
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { visits++ }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer server.Close()
	signed := api.ObjectSignedRequest{URL: server.URL + "?private=capability-secret", Method: "PUT", ExpiresAt: time.Now().Add(time.Minute)}
	response, err := executeBucketCapability(t.Context(), signed, "PUT", strings.NewReader("x"), 1)
	if response != nil {
		_ = response.Body.Close()
	}
	if err == nil || strings.Contains(err.Error(), "capability-secret") || visits != 0 {
		t.Fatal(err, visits)
	}
	signed.Headers = map[string]string{"Authorization": "Bearer forbidden"}
	response, err = executeBucketCapability(t.Context(), signed, "PUT", strings.NewReader("x"), 1)
	if response != nil {
		_ = response.Body.Close()
	}
	if err == nil || visits != 0 {
		t.Fatal(err, visits)
	}
}

// adr: 628
func TestBucketMultipartMalformedGeometryPreservesSession(t *testing.T) {
	file := filepath.Join(t.TempDir(), "input")
	_ = os.WriteFile(file, []byte("abcdefg"), 0600)
	c := &transferFixture{session: api.ObjectMultipartUpload{ID: uuid.NewString(), Key: "key", SizeBytes: 7, PartSizeBytes: 1 << 62, PartCount: 1}}
	result, err := runBucketTransfer(t.Context(), c, bucketTransferOptions{action: "upload", app: "demo", bucket: uuid.NewString(), key: "key", path: file})
	if err == nil || result.UploadID != c.session.ID || result.Status != "pending" || c.calls != 1 {
		t.Fatal(result, err, c.calls)
	}
}

// adr: 628
func TestBucketUploadRefusesNonRegularSourcesBeforeAdmission(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "source")
	if err := os.WriteFile(file, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir, link} {
		c := &transferFixture{}
		if _, err := runBucketTransfer(t.Context(), c, bucketTransferOptions{action: "upload", path: path}); err == nil || c.calls != 0 {
			t.Fatal("non-regular source admitted", err, c.calls)
		}
	}
}

// adr: 628
func TestObjectStorageUsageCLIReportsUnknownMeters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/account/object-storage-usage" {
			t.Errorf("usage path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(api.ObjectStorageUsageResponse{Usage: api.ObjectStorageUsage{UnavailableMeters: []string{"requests", "egress"}}, BillingMode: "disabled"})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	var out bytes.Buffer
	oldOut, oldJSON := osStdout, jsonOutput
	osStdout, jsonOutput = &out, false
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	if code := cmdUsageObjectStorage(nil); code != 0 || !strings.Contains(out.String(), "Unavailable meters") {
		t.Fatal(code, out.String())
	}
}

// adr: 628
func TestRunObjectStorageUsageJSONPreservesUnknownMeters(t *testing.T) {
	want := api.ObjectStorageUsageResponse{Usage: api.ObjectStorageUsage{UnavailableMeters: []string{"requests", "egress"}}, BillingMode: "off"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/account/object-storage-usage" {
			t.Errorf("usage request %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(want)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var out bytes.Buffer
	oldOut, oldJSON := osStdout, jsonOutput
	osStdout, jsonOutput = &out, false
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	if code := run([]string{"usage", "object-storage", "--json"}); code != 0 {
		t.Fatal(code, out.String())
	}
	var got api.ObjectStorageUsageResponse
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err, out.String())
	}
	if got.BillingMode != want.BillingMode || got.Usage.Fresh || got.Charges != nil || strings.Join(got.Usage.UnavailableMeters, ",") != "requests,egress" {
		t.Fatal(got)
	}
}
