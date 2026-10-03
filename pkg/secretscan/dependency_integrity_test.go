package secretscan

import (
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

func TestNpmIntegrityDoesNotHideCredentials(t *testing.T) {
	digest := sha512.Sum512([]byte("reproducible MCP dependency"))
	integrity := "sha512-" + base64.StdEncoding.EncodeToString(digest[:])
	const token = "A7qDm9PzR3uK8vN2xL5cW0sY6hF4eB1j"
	providerFixture := strings.Join([]string{"sk", "live", "1234567890abcdefghijklmn"}, "_")
	for _, tc := range []struct {
		name, path, value, extra string
		want                     int
	}{
		{"checksum", "package-lock.json", integrity, "", 0},
		{"shrinkwrap", "npm-shrinkwrap.json", integrity, "", 0},
		{"ordinary source", "source.json", integrity, "", 1},
		{"not a checksum", "package-lock.json", token, "", 1},
		{"credential in lock", "package-lock.json", integrity, `,"token":"` + token + `"`, 1},
		{"provider in integrity", "package-lock.json", providerFixture, "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf("{\n\"lockfileVersion\":3,\n\"packages\":{\"dep\":{\n\"integrity\":%q\n}}%s\n}", tc.value, tc.extra)
			got := ScanFile(tc.path, []byte(body))
			if len(got) != tc.want {
				t.Fatalf("got %d findings, want %d: %+v", len(got), tc.want, got)
			}
		})
	}
	if got := ScanFile("package-lock.json", []byte(fmt.Sprintf("\"integrity\":%q", integrity))); len(got) != 1 {
		t.Fatal("non-lockfile content suppressed")
	}
}
