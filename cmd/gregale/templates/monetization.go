package templates

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261018090210 — a starter's API monetization parts ship only while
// monetization is released (FAAS_API_MONETIZATION_ENABLED, as for the CLI's
// monetization verbs): its files are left out, and README blocks between the
// monetization markers are cut. The embedded tree stays complete for tests.
const (
	monetizationStart = "<!-- gregale:api-monetization -->\n"
	monetizationEnd   = "<!-- /gregale:api-monetization -->\n"
)

// monetizationFiles are, per template, the files only API monetization uses.
var monetizationFiles = map[string][]string{
	"customer-platform": {"tools/billing.js", "test/billing.test.js"},
}

// shipped reports whether a template file ships and returns its content as
// shipped. Only README.md blocks are rewritten.
func shipped(name, file string, content []byte) ([]byte, bool) {
	if api.APIMonetizationEnabled() {
		return content, true
	}
	if slices.Contains(monetizationFiles[name], file) {
		return nil, false
	}
	if file == "README.md" {
		return withoutMonetizationBlocks(content), true
	}
	return content, true
}

// withoutMonetizationBlocks cuts every marked block, markers included.
func withoutMonetizationBlocks(content []byte) []byte {
	for {
		start := bytes.Index(content, []byte(monetizationStart))
		if start < 0 {
			return content
		}
		end := bytes.Index(content[start:], []byte(monetizationEnd))
		if end < 0 {
			return content[:start]
		}
		content = append(content[:start:start], content[start+end+len(monetizationEnd):]...)
	}
}

// withholdMonetization applies shipped to a materialized template.
func withholdMonetization(name, dest string) error {
	if api.APIMonetizationEnabled() {
		return nil
	}
	for _, file := range monetizationFiles[name] {
		if err := os.Remove(filepath.Join(dest, filepath.FromSlash(file))); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	readme := filepath.Join(dest, "README.md")
	content, err := os.ReadFile(readme)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if trimmed := withoutMonetizationBlocks(content); !bytes.Equal(trimmed, content) {
		return os.WriteFile(readme, trimmed, 0o644)
	}
	return nil
}
