package oci

import "testing"

func TestParseReference(t *testing.T) {
	cases := []struct {
		in       string
		registry string
		repo     string
		tag      string
		digest   string
		apiHost  string
	}{
		{
			in: "nginx", registry: "docker.io", repo: "library/nginx", tag: "latest",
			apiHost: "registry-1.docker.io",
		},
		{
			in: "nginx:1.25", registry: "docker.io", repo: "library/nginx", tag: "1.25",
			apiHost: "registry-1.docker.io",
		},
		{
			in: "org/app:v2", registry: "docker.io", repo: "org/app", tag: "v2",
			apiHost: "registry-1.docker.io",
		},
		{
			in: "ghcr.io/org/app:main", registry: "ghcr.io", repo: "org/app", tag: "main",
			apiHost: "ghcr.io",
		},
		{
			in:       "ghcr.io/org/app@sha256:" + hex64,
			registry: "ghcr.io", repo: "org/app", tag: "", digest: "sha256:" + hex64,
			apiHost: "ghcr.io",
		},
		{
			// tag + digest both present.
			in:       "ghcr.io/org/app:main@sha256:" + hex64,
			registry: "ghcr.io", repo: "org/app", tag: "main", digest: "sha256:" + hex64,
			apiHost: "ghcr.io",
		},
		{
			// registry with a port must not be mistaken for a tag.
			in: "localhost:5000/app:dev", registry: "localhost:5000", repo: "app", tag: "dev",
			apiHost: "localhost:5000",
		},
		{
			in: "registry.example.com/team/svc", registry: "registry.example.com",
			repo: "team/svc", tag: "latest", apiHost: "registry.example.com",
		},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			r, err := ParseReference(tc.in)
			if err != nil {
				t.Fatalf("ParseReference(%q): %v", tc.in, err)
			}
			if r.Registry != tc.registry || r.Repository != tc.repo || r.Tag != tc.tag || r.Digest != tc.digest {
				t.Errorf("got %+v", r)
			}
			if r.APIHost() != tc.apiHost {
				t.Errorf("APIHost = %q, want %q", r.APIHost(), tc.apiHost)
			}
		})
	}
}

func TestReferenceManifestRefAndString(t *testing.T) {
	r, _ := ParseReference("ghcr.io/org/app:main@sha256:" + hex64)
	if r.ManifestRef() != "sha256:"+hex64 {
		t.Errorf("ManifestRef = %q, want the digest", r.ManifestRef())
	}
	r2, _ := ParseReference("ghcr.io/org/app:main")
	if r2.ManifestRef() != "main" {
		t.Errorf("ManifestRef = %q, want the tag", r2.ManifestRef())
	}
	if got := r.String(); got != "ghcr.io/org/app:main@sha256:"+hex64 {
		t.Errorf("String = %q", got)
	}
}

func TestParseReferenceErrors(t *testing.T) {
	bad := []string{
		"",
		"   ",
		"app@sha256:tooshort",
		"app@md5:" + hex64,                      // unsupported algo
		"app@sha256:" + hex64 + "z",             // wrong length
		"ghcr.io/org/app@sha256:zz" + hex64[2:], // non-hex
	}
	for _, in := range bad {
		if _, err := ParseReference(in); err == nil {
			t.Errorf("ParseReference(%q) = nil error, want error", in)
		}
	}
}

// hex64 is a valid 64-char lowercase-hex sha256 body.
const hex64 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
