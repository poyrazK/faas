package templates

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// platformInternals are component names a customer never runs or sees.
var platformInternals = regexp.MustCompile(`\b(apid|guest-init|vmmd|schedd|gatewayd|jailer)\b`)

// TestHelloTemplatesSpeakToCustomers pins the first-run copy of the hello
// starters. Their README is read inside a directory that start or init has
// already created, so it leads with `gregale start` there. No file names a
// platform component. cmd/gregale's template_readme_test.go pins the rest of
// the README contract.
func TestHelloTemplatesSpeakToCustomers(t *testing.T) {
	for _, name := range []string{"hello-node", "hello-python", "hello-go"} {
		t.Run(name, func(t *testing.T) {
			readme, err := fs.ReadFile(FS, name+"/README.md")
			if err != nil {
				t.Fatal(err)
			}
			text := string(readme)
			if !strings.Contains(text, "From this directory:\n\n```\ngregale start\n```") {
				t.Errorf("README must lead with gregale start from this directory:\n%s", text)
			}
			err = fs.WalkDir(FS, name, func(path string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				data, err := fs.ReadFile(FS, path)
				if err != nil {
					return err
				}
				if match := platformInternals.Find(data); match != nil {
					t.Errorf("%s names platform internal %q", path, match)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
