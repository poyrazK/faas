// adr: 20261027090013 §6
package api

import "testing"

func TestSandboxRegistryHostMatchesDomainsAndSubdomainsOnly(t *testing.T) {
	for name, want := range map[string]bool{
		"registry.npmjs.org": true, "registry.npmjs.org.": true, "PyPI.org": true, "github.com": true,
		"codeload.github.com": true, "raw.githubusercontent.com": true, "static.crates.io": true,
		"dl-cdn.alpinelinux.org": true, "deb.debian.org": true,
		"": false, "example.com": false, "evilgithub.com": false, "github.com.evil.net": false,
		"debian.org": false, "ubuntu.com": false, "golang.org": false, "svc.gregale": false,
	} {
		if got := SandboxRegistryHost(name); got != want {
			t.Errorf("SandboxRegistryHost(%q) = %v, want %v", name, got, want)
		}
	}
}
