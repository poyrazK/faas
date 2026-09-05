package oci

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInspectImageMetadataOnly(t *testing.T) {
	config := []byte(`{"os":"linux","architecture":"amd64","rootfs":{"type":"layers"},"config":{"Entrypoint":["node"],"Cmd":["server.js"],"User":"1000","WorkingDir":"/app","ExposedPorts":{"8080/tcp":{}},"Volumes":{"/data":{}}}}`)
	configDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(config))
	manifest := fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"digest":%q},"layers":[{"digest":"sha256:%s"}]}`, configDigest, strings.Repeat("a", 64))
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(manifest)))
	requests := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/v2/org/app/manifests/latest", "/v2/org/app/manifests/" + digest:
			_, _ = fmt.Fprint(w, manifest)
		case "/v2/org/app/blobs/" + configDigest:
			_, _ = w.Write(config)
		default:
			t.Errorf("unexpected request (layers must never be fetched): %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := NewRegistryClient(WithEndpoint("http", strings.TrimPrefix(srv.URL, "http://")))
	for _, ref := range []string{"example.com/org/app:latest", "example.com/org/app@" + digest} {
		got, err := c.InspectImage(context.Background(), ref, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.Digest != digest || got.Reference != "example.com/org/app@"+digest {
			t.Fatalf("wrong immutable reference: %+v", got)
		}
		if got.Config.OS != "linux" || got.Config.Architecture != "amd64" || got.Config.Entrypoint[0] != "node" || got.Config.Cmd[0] != "server.js" || len(got.Config.Volumes) != 1 || len(got.Config.ExposedPorts) != 1 {
			t.Fatalf("lost config metadata: %+v", got.Config)
		}
	}
	if len(requests) != 4 {
		t.Fatalf("expected two metadata requests per inspection, got %v", requests)
	}
}

func TestInspectImageRejectsInvalidContent(t *testing.T) {
	for _, tc := range []struct{ name, body, pinned string }{
		{"index", `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}`, ""},
		{"malformed", `{`, ""},
		{"no config", `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","layers":[{}]}`, ""},
		{"bad config hash", fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"digest":"sha256:%s"},"layers":[{"digest":"sha256:%s"}]}`, strings.Repeat("a", 64), strings.Repeat("b", 64)), ""},
		{"wrong manifest hash", `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`, "@sha256:" + strings.Repeat("a", 64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/blobs/") {
					_, _ = fmt.Fprint(w, `{}`)
					return
				}
				_, _ = fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			c := NewRegistryClient(WithEndpoint("http", strings.TrimPrefix(srv.URL, "http://")))
			_, err := c.InspectImage(context.Background(), "example.com/org/app"+tc.pinned, nil)
			if err == nil {
				t.Fatal("accepted invalid metadata")
			}
		})
	}
}

func TestInspectImagePrivateRegistryAndRedaction(t *testing.T) {
	auth := &BasicAuth{Username: "private-user", Password: "private-password"}
	var tokenRequested bool
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			tokenRequested = true
			u, p, ok := r.BasicAuth()
			if !ok || u != auth.Username || p != auth.Password {
				t.Error("credentials missing from token request")
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "opaque-token"})
			return
		}
		if r.Header.Get("Authorization") == "" {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm=%q,service="registry",scope="repository:org/app:pull"`, srv.URL+"/token"))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprintf(w, "denied %s %s Authorization: Bearer opaque-token", auth.Username, auth.Password)
	}))
	defer srv.Close()
	c := NewRegistryClient(WithEndpoint("http", strings.TrimPrefix(srv.URL, "http://")))
	_, err := c.InspectImage(context.Background(), "example.com/org/app", auth)
	if err == nil || !tokenRequested {
		t.Fatalf("expected authenticated failure, got %v", err)
	}
	for _, secret := range []string{auth.Username, auth.Password, "opaque-token"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaked credential: %s", secret)
		}
	}
}

func TestInspectImageCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewRegistryClient().InspectImage(ctx, "example.com/org/app", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}
