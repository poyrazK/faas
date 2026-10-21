// adr: 650 — the Data API image must carry every module its server imports.
package main_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Node tests import modules from the template directory, so a module the
// Dockerfile forgets to copy passes every unit test and breaks the image.
// Development-only modules (dev*.mjs, the inspector) are deliberately left
// out of the image, so this follows server.mjs's imports instead of listing
// the directory.
func TestDataAPITemplateImageCopiesEveryServerImport(t *testing.T) {
	dockerfile, err := os.ReadFile("templates/data-api/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	copied := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^COPY ([^\n]+) \./$`).FindAllStringSubmatch(string(dockerfile), -1) {
		for _, name := range strings.Fields(m[1]) {
			copied[name] = true
		}
	}
	local := regexp.MustCompile(`(?m)^import [^\n]* from '\./([a-z0-9-]+\.mjs)'`)
	seen := map[string]bool{}
	queue := []string{"server.mjs"}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if seen[name] {
			continue
		}
		seen[name] = true
		if !copied[name] {
			t.Errorf("Dockerfile does not copy %s, which the server imports", name)
		}
		source, err := os.ReadFile("templates/data-api/" + name)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range local.FindAllStringSubmatch(string(source), -1) {
			queue = append(queue, m[1])
		}
	}
	if !seen["storage.mjs"] {
		t.Fatal("the import walk missed storage.mjs; the pattern no longer matches server.mjs")
	}
}
