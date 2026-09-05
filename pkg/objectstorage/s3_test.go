package objectstorage

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func testCredentials(name string) string {
	if name == "KEY" {
		return "test-key"
	}
	if name == "SECRET" {
		return "test-secret"
	}
	return ""
}
func testBackend() BackendConfig {
	return BackendConfig{ID: "external-a", Driver: "s3", Region: "us-east-1", Namespace: "test-project", Endpoint: "https://s3.example.test", S3Region: "us-east-1", PathStyle: true, AccessKeyEnv: "KEY", SecretKeyEnv: "SECRET"}
}

func TestS3Presign(t *testing.T) {
	p, err := NewS3(testBackend(), testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int64{0, 1, 100 << 20} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			out, err := p.Presign(context.Background(), "gregale-test", SignRequest{Method: "PUT", Key: "folder/hello +世界.txt", SizeBytes: &size})
			if err != nil {
				t.Fatalf("size %d: %v", size, err)
			}
			u, err := url.Parse(out.URL)
			if err != nil {
				t.Fatal(err)
			}
			header := "content-length"
			if size == 0 {
				header = "content-md5"
			}
			if !strings.Contains(u.Query().Get("X-Amz-SignedHeaders"), header) {
				t.Fatal("unsigned upload length", out.Headers)
			}
			if strings.Contains(out.URL, "test-secret") || u.Path != "/gregale-test/folder/hello +世界.txt" {
				t.Fatal("incorrect URL")
			}
		})
	}
	out, err := p.Presign(context.Background(), "gregale-test", SignRequest{Method: "GET", Key: "index.html", ExpiresIn: 60})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(out.URL)
	if u.Query().Get("response-content-disposition") != "attachment" || u.Query().Get("X-Amz-Expires") != "60" {
		t.Fatal("unsafe download")
	}
}

func TestS3ProtocolAndErrors(t *testing.T) {
	var calls []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		if r.Header.Get("Authorization") == "" {
			t.Error("missing upstream signature")
		}
		switch {
		case r.Method == "PUT":
			body, _ := io.ReadAll(r.Body)
			if r.URL.Query().Has("cors") {
				digest := md5.Sum(body)
				if r.Header.Get("Content-MD5") != base64.StdEncoding.EncodeToString(digest[:]) {
					t.Error("missing CORS checksum")
				}
				if r.Header.Get("X-Amz-Checksum-Crc32") != "" || r.Header.Get("X-Amz-Sdk-Checksum-Algorithm") != "" {
					t.Error("AWS-only CORS checksum")
				}
			}
			if r.URL.RawQuery == "" && strings.Contains(string(body), "LocationConstraint") {
				t.Error("unexpected AWS us-east-1 location")
			}
			w.WriteHeader(200)
		case r.Method == "GET":
			w.Header().Set("Content-Type", "application/xml")
			io.WriteString(w, `<ListBucketResult><IsTruncated>true</IsTruncated><NextContinuationToken>next</NextContinuationToken><Contents><Key>folder/file</Key><Size>3</Size><LastModified>2026-09-05T00:00:00Z</LastModified></Contents></ListBucketResult>`)
		case r.Method == "DELETE":
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(409)
			io.WriteString(w, `<Error><Code>BucketNotEmpty</Code><Message>private-provider-diagnostic</Message></Error>`)
		}
	}))
	defer upstream.Close()
	c := testBackend()
	c.Endpoint = upstream.URL
	c.AllowedOrigins = []string{"https://console.example.test"}
	p, err := NewS3(c, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.CreateBucket(context.Background(), "gregale-test"); err != nil {
		t.Fatal(err)
	}
	page, err := p.ListObjects(context.Background(), "gregale-test", "folder/", "opaque+cursor", 10)
	if err != nil || len(page.Items) != 1 || page.Items[0].Size != 3 || page.NextCursor != "next" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if err = p.DeleteBucket(context.Background(), "gregale-test"); !errors.Is(err, ErrNotEmpty) {
		t.Fatal(err)
	}
	if len(calls) != 4 || !strings.Contains(calls[1], "cors") || !strings.Contains(calls[2], "continuation-token=opaque%2Bcursor") {
		t.Fatal(calls)
	}
}

func TestRegistryProviderSwitch(t *testing.T) {
	a := testBackend()
	b := a
	b.ID = "self-hosted"
	b.Namespace = "ceph-one"
	b.Endpoint = "https://ceph.example.test"
	c := Config{DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": a.ID}, Backends: []BackendConfig{a, b}}
	factories := map[string]Factory{"s3": NewS3}
	r, err := NewRegistry(c, testCredentials, factories)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := r.Default("us-east-1")
	c.Defaults["us-east-1"] = b.ID
	r, err = NewRegistry(c, testCredentials, factories)
	if err != nil {
		t.Fatal(err)
	}
	newBackend, _ := r.Default("us-east-1")
	if newBackend.ID != b.ID {
		t.Fatal(newBackend)
	}
	resolved, err := r.Resolve(old.ID, old.Fingerprint)
	if err != nil || resolved.ID != a.ID {
		t.Fatal("old bucket moved", err)
	}
	c.Backends[0].Endpoint = "https://wrong.example.test"
	r, err = NewRegistry(c, testCredentials, factories)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Resolve(old.ID, old.Fingerprint); !errors.Is(err, ErrUnavailable) {
		t.Fatal("placement change did not fence old buckets")
	}
}

func TestValidation(t *testing.T) {
	for _, endpoint := range []string{"http://s3.example.test", "https://user:secret@s3.example.test", "https://s3.example.test/path", "https://s3.example.test?secret=x"} {
		c := testBackend()
		c.Endpoint = endpoint
		_, err := NewRegistry(Config{DefaultRegion: c.Region, Defaults: map[string]string{c.Region: c.ID}, Backends: []BackendConfig{c}}, testCredentials, map[string]Factory{"s3": NewS3})
		if err == nil {
			t.Fatal("accepted", endpoint)
		}
	}
	n := int64(101)
	for _, req := range []SignRequest{{Method: "PUT", Key: "k"}, {Method: "PUT", Key: "k", SizeBytes: &n}, {Method: "DELETE", Key: "k"}, {Method: "GET", Key: ""}, {Method: "GET", Key: "a\n"}, {Method: "GET", Key: "k", ExpiresIn: 901}, {Method: "GET", Key: "k", SizeBytes: &n}} {
		if req.Validate(100) == nil {
			t.Fatalf("accepted %+v", req)
		}
	}
}
