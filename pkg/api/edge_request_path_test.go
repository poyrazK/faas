package api

import "testing"

func TestNormalizeEdgeRequestPath(t *testing.T) {
	for in, want := range map[string]string{
		"/":                 "/",
		"/a/b":              "/a/b",
		"/a/b/":             "/a/b/",
		"/public/../upload": "/upload",
		"//upload":          "/upload",
		"/a//b///c":         "/a/b/c",
		"/a/./b":            "/a/b",
		"/a/b/..":           "/a/",
		"/a/b/.":            "/a/b/",
		"/../../x":          "/x",
		"/a/../":            "/",
		"/.well-known/x":    "/.well-known/x",
		"/a/.hidden/":       "/a/.hidden/",
		`/a\..\b`:           `/a\..\b`,
		"/a;x/b":            "/a;x/b",
		"*":                 "*",
	} {
		if got := NormalizeEdgeRequestPath(in); got != want {
			t.Errorf("NormalizeEdgeRequestPath(%q) = %q, want %q", in, got, want)
		}
	}
}
