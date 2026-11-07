package objectstorage

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261104221012: an upload route admits an active type only when it names
// it exactly, never through a wildcard or an empty allowlist.
func TestUploadRouteAdmitsActiveTypesOnlyByName(t *testing.T) {
	for _, tc := range []struct {
		allowed []string
		ct      string
		want    bool
	}{
		{allowed: nil, ct: "image/png", want: true},
		{allowed: nil, ct: "text/html", want: false},
		{allowed: nil, ct: "application/javascript", want: false},
		{allowed: []string{"image/*"}, ct: "image/png", want: true},
		{allowed: []string{"image/*"}, ct: "image/svg+xml", want: false},
		{allowed: []string{"application/*"}, ct: "application/xhtml+xml", want: false},
		{allowed: []string{"application/*"}, ct: "application/rss+xml", want: false},
		{allowed: []string{"image/svg+xml"}, ct: "image/svg+xml", want: true},
		{allowed: []string{"text/html"}, ct: "text/html", want: true},
	} {
		if got := allowedUploadContentType(tc.allowed, tc.ct); got != tc.want {
			t.Errorf("allowedUploadContentType(%v, %q) = %v, want %v", tc.allowed, tc.ct, got, tc.want)
		}
	}
}

func TestActiveContentType(t *testing.T) {
	for ct, want := range map[string]bool{
		"text/html":                 true,
		"TEXT/HTML; charset=utf-8":  true,
		"image/svg+xml":             true,
		"application/atom+xml":      true,
		"text/javascript":           true,
		"multipart/x-mixed-replace": true,
		"image/png":                 false,
		"application/json":          false,
		"application/pdf":           false,
		"text/plain":                false,
	} {
		if got := ActiveContentType(ct); got != want {
			t.Errorf("ActiveContentType(%q) = %v, want %v", ct, got, want)
		}
	}
}

// A public SVG or XML object opened directly runs sandboxed; a page or an
// image does not get the policy.
func TestPublicObjectHeadersSandboxSVGAndXML(t *testing.T) {
	for ct, want := range map[string]bool{
		"image/svg+xml":            true,
		"application/xml":          true,
		"text/html; charset=utf-8": false,
		"application/javascript":   false,
		"image/png":                false,
	} {
		dst := http.Header{}
		copyPublicObjectHeaders(dst, http.Header{"Content-Type": {ct}})
		if got := dst.Get("Content-Security-Policy") == publicObjectSandboxCSP; got != want {
			t.Errorf("%s: sandboxed = %v, want %v", ct, got, want)
		}
	}
}

func TestPublicServed(t *testing.T) {
	if !PublicServed(state.ObjectBucket{PublicRead: true, ServeAt: "/assets"}) {
		t.Error("a public mounted bucket is not public served")
	}
	if PublicServed(state.ObjectBucket{PublicRead: true}) || PublicServed(state.ObjectBucket{ServeAt: "/assets"}) {
		t.Error("an unmounted or private bucket is public served")
	}
}
