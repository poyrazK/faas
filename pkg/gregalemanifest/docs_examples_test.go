package gregalemanifest

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var (
	docsYAMLFence     = regexp.MustCompile("^```ya?ml\\s*$")
	docsOtherFileHint = regexp.MustCompile(`^#\s*\S+\.(ya?ml|json|toml)\b`)
)

// docsManifestExamples returns the YAML blocks in the customer docs that
// present themselves as gregale.yaml: the block's first line names the file,
// or one of the four lines before it does.
func docsManifestExamples(t *testing.T) map[string]string {
	t.Helper()
	root := filepath.Join("..", "..", "docs")
	examples := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "adr" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines := strings.Split(string(raw), "\n")
		for i := 0; i < len(lines); i++ {
			if !docsYAMLFence.MatchString(lines[i]) {
				continue
			}
			end := i + 1
			for end < len(lines) && !strings.HasPrefix(lines[end], "```") {
				end++
			}
			body := lines[i+1 : min(end, len(lines))]
			first := ""
			for _, line := range body {
				if strings.TrimSpace(line) != "" {
					first = strings.TrimSpace(line)
					break
				}
			}
			before := strings.Join(lines[max(0, i-4):i], " ")
			named := strings.Contains(first, "gregale.yaml") ||
				(strings.Contains(before, "gregale.yaml") && !docsOtherFileHint.MatchString(first))
			if named {
				rel, _ := filepath.Rel(root, path)
				examples[rel+":"+strconv.Itoa(i+1)] = strings.Join(body, "\n") + "\n"
			}
			i = end
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk docs: %v", err)
	}
	return examples
}

// Every gregale.yaml example in the customer docs must parse with the
// strict manifest parser the CLI uses. docs/deploy-overrides.md opened with
// `name: checkout-api`, which the parser rejects as an unknown key, so the
// first example a customer copied failed (Kroki feedback 2026-11-17).
func TestDocsManifestExamplesParse(t *testing.T) {
	examples := docsManifestExamples(t)
	if len(examples) < 10 {
		t.Fatalf("found %d gregale.yaml examples in docs; the selector is stale", len(examples))
	}
	for where, body := range examples {
		if _, err := ParseBytes([]byte(body)); err != nil {
			t.Errorf("docs/%s: gregale.yaml example does not parse: %v", where, err)
		}
	}
}
