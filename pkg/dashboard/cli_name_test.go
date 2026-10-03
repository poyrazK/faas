package dashboard

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// TestTemplates_NameTheShippedCLI — dashboard pages told customers to run
// `faas plan`, `faas billing portal`, `faas account restore`, `faas keys
// create`, … The CLI binary is `gregale` (and the key verb is `keys add`),
// so every copy-pasted hint failed with "command not found".
func TestTemplates_NameTheShippedCLI(t *testing.T) {
	t.Parallel()
	stale := regexp.MustCompile(`(<code>|<pre>)\s*faas\s`)
	err := fs.WalkDir(tmplFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".html") {
			return err
		}
		raw, err := fs.ReadFile(tmplFS, path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if stale.MatchString(line) {
				t.Errorf("%s:%d tells the customer to run a `faas` command; the CLI is gregale:\n%s", path, i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
