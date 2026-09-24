package preflight

import "testing"

// The parser is the SSRF boundary. Preflight never fetches a user-supplied
// URL: it extracts owner/repo and builds every upstream URL itself, so a
// hostile input cannot steer the request at all.
func TestParseSource_AcceptsGitHubForms(t *testing.T) {
	cases := []struct {
		in    string
		owner string
		repo  string
	}{
		{"https://github.com/gregale/api", "gregale", "api"},
		{"https://github.com/gregale/api.git", "gregale", "api"},
		{"https://github.com/gregale/api/tree/main", "gregale", "api"},
		{"https://www.github.com/gregale/api", "gregale", "api"},
		{"github.com/gregale/api", "gregale", "api"},
		{"gregale/api", "gregale", "api"},
		{"git@github.com:gregale/api.git", "gregale", "api"},
		{"  https://github.com/gregale/api/  ", "gregale", "api"},
		{"https://github.com/Gregale/Api-Server_v2.0", "Gregale", "Api-Server_v2.0"},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			src, err := ParseSource(tc.in)
			if err != nil {
				t.Fatalf("ParseSource(%q) error: %v", tc.in, err)
			}
			if src.Owner != tc.owner || src.Repo != tc.repo {
				t.Errorf("got %s/%s, want %s/%s", src.Owner, src.Repo, tc.owner, tc.repo)
			}
		})
	}
}

// Anything that is not a github.com repository is refused before a socket is
// opened. These are the inputs an attacker actually sends.
func TestParseSource_RejectsHostileInput(t *testing.T) {
	hostile := []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]:8080/a/b",
		"http://127.0.0.1/a/b",
		"https://10.0.0.1/a/b",
		"https://evil.example/gregale/api",
		"https://github.com.evil.example/gregale/api",
		"https://evilgithub.com/gregale/api",
		"https://user:token@github.com/gregale/api",
		"file:///etc/passwd",
		"gopher://github.com/gregale/api",
		"https://github.com/gregale",
		"https://github.com/",
		"../../etc/passwd",
		"gregale/api/../../secrets",
		"gregale/../api",
		"-flag/repo",
		"",
		"   ",
		"/",
	}

	for _, in := range hostile {
		t.Run(in, func(t *testing.T) {
			if src, err := ParseSource(in); err == nil {
				t.Fatalf("ParseSource(%q) accepted as %s/%s, want rejection", in, src.Owner, src.Repo)
			}
		})
	}
}

// A repository name long enough to be an overflow probe is refused rather
// than forwarded upstream.
func TestParseSource_RejectsOverlongInput(t *testing.T) {
	long := make([]byte, 4096)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := ParseSource("gregale/" + string(long)); err == nil {
		t.Fatal("overlong repo name accepted, want rejection")
	}
}
