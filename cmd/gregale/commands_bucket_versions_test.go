package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 687
func TestBucketVersionsCLIJSONAndPagination(t *testing.T) {
	bucket, version := uuid.NewString(), uuid.NewString()
	want := api.ObjectVersionList{Items: []api.ObjectVersion{{Key: "目录 +%.txt", VersionID: version}}, CommonPrefixes: []string{"目录/"}, NextKeyMarker: "目录 +%.txt", NextVersionIDMarker: version}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method != "GET" || r.URL.Path != "/v1/apps/demo/buckets/"+bucket+"/objects/versions" || q.Get("prefix") != "目录" || q.Get("key_marker") != want.NextKeyMarker || q.Get("version_id_marker") != version || q.Get("delimiter") != "/" || q.Get("limit") != "2" {
			t.Errorf("wrong listing request: %s", r.URL)
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
	if code := run([]string{"bucket", "versions", "list", "demo", bucket, "--prefix", "目录", "--delimiter", "/", "--limit", "2", "--key-marker", want.NextKeyMarker, "--version-id-marker", version, "--json"}); code != 0 {
		t.Fatal(code, out.String())
	}
	var got api.ObjectVersionList
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || len(got.Items) != 1 || got.Items[0].VersionID != version || got.Items[0].Key != want.NextKeyMarker || got.NextVersionIDMarker != version || len(got.CommonPrefixes) != 1 {
		t.Fatal(got, err)
	}
	for _, flags := range [][]string{{"--limit", "0"}, {"--delimiter", "//"}, {"--version-id-marker", version}, {"--key-marker", "key", "--version-id-marker", "private-native"}, {"--prefix", "bad\nkey"}} {
		if _, _, _, e := parseBucketVersions(append([]string{"list", "demo", bucket}, flags...)); e == nil {
			t.Fatal("invalid flags admitted", flags)
		}
	}
}

// adr: 687
func TestBucketHistoricalDownloadChecksAcknowledgedVersion(t *testing.T) {
	version := uuid.NewString()
	for _, headers := range [][]string{{version}, nil, {uuid.NewString()}, {"native-generation"}, {version, version}} {
		t.Run(strings.Join(headers, ","), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				for _, v := range headers {
					w.Header().Add("X-Amz-Version-Id", v)
				}
				_, _ = w.Write([]byte("historical"))
			}))
			defer server.Close()
			file := filepath.Join(t.TempDir(), "download")
			if err := os.WriteFile(file, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			c := &transferFixture{url: server.URL}
			result, err := runBucketTransfer(t.Context(), c, bucketTransferOptions{action: "download", app: "demo", bucket: uuid.NewString(), key: "key", path: file, force: true, versionID: version})
			success := len(headers) == 1 && headers[0] == version
			if (err == nil) != success || c.request.VersionID != version {
				t.Fatal(result, err, c.request)
			}
			data, _ := os.ReadFile(file)
			if success && (string(data) != "historical" || result.VersionID != version) || !success && string(data) != "original" {
				t.Fatal("incorrect download publication", result, string(data))
			}
		})
	}
	if _, err := parseBucketTransfer([]string{"download", "demo", uuid.NewString(), "key", "path", "--version-id", "null"}); err == nil {
		t.Fatal("mutable null version accepted")
	}
}
