package main

import (
	"os"
	"strings"
	"testing"
)

// The restore warnings link to this section; renaming its heading breaks
// the link in every customer's logs.
func TestReseedDocsURLNamesAnExistingSection(t *testing.T) {
	body, err := os.ReadFile("../../docs/container-compatibility.md")
	if err != nil {
		t.Fatal(err)
	}
	_, anchor, ok := strings.Cut(reseedDocsURL, "#")
	if !ok {
		t.Fatalf("%s has no section anchor", reseedDocsURL)
	}
	heading := "## " + strings.ToUpper(anchor[:1]) + strings.ReplaceAll(anchor[1:], "-", " ")
	if !strings.Contains(string(body), "\n"+heading+"\n") {
		t.Fatalf("docs/container-compatibility.md has no %q heading for %s", heading, reseedDocsURL)
	}
}
