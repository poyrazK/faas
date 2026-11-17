// adr: 20261112121456 §2
package api

import "testing"

func TestSandboxWebHostAdmitsPublicNamesOnly(t *testing.T) {
	for name, want := range map[string]bool{
		"example.com": true, "www.example.com.": true, "API.GitHub.com": true, "a.b.co.uk": true,
		"_dmarc.example.org": true, "xn--bcher-kva.de": true, "1e100.net": true,
		"": false, "localhost": false, "com": false, "api.svc.gregale": false, "db.internal": false,
		"printer.local": false, "1.0.0.10.in-addr.arpa": false, "foo.test": false, "x.onion": false,
		"router.lan": false, "box.home": false, "10.0.0.1": false, "::1": false, "1.2.3.4.5": false,
		"-bad.com": false, "bad-.com": false, "a..com": false, "sp ace.com": false, "ex@mple.com": false,
	} {
		if got := SandboxWebHost(name); got != want {
			t.Errorf("SandboxWebHost(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestValidSandboxBrowseURL(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://example.com/docs?q=1": true, "http://www.example.org": true, "https://example.com:443/": true,
		"": false, "ftp://example.com": false, "https://user:pw@example.com": false, "https://localhost/": false,
		"https://10.0.0.1/": false, "https://api.svc.gregale/": false, "https://example.com:8080/": false,
		"javascript:alert(1)": false, "https://exa mple.com": false, "file:///etc/passwd": false,
	} {
		if got := ValidSandboxBrowseURL(raw); got != want {
			t.Errorf("ValidSandboxBrowseURL(%q) = %v, want %v", raw, got, want)
		}
	}
}
