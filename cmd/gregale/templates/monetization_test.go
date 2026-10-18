package templates

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// adr: 20261018090210 — customer-platform ships its billing review only while
// API monetization is released; the operator tools still run without it.
func TestCustomerPlatformBillingFollowsMonetization(t *testing.T) {
	for _, released := range []bool{false, true} {
		value := ""
		if released {
			value = "1"
		}
		t.Setenv("FAAS_API_MONETIZATION_ENABLED", value)
		dir, cleanup, err := MaterializeForTest("customer-platform")
		if err != nil {
			t.Fatal(err)
		}
		_, billingErr := os.Stat(filepath.Join(dir, "tools", "billing.js"))
		readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, "tools", "customer.js")); err != nil {
			t.Fatal(err)
		}
		cleanup()
		if (billingErr == nil) != released || strings.Contains(string(readme), "billing month") != released ||
			!released && strings.Contains(string(readme), "gregale:api-monetization") {
			t.Fatalf("released=%t: billing.js err=%v, README:\n%s", released, billingErr, readme)
		}

		archive := filepath.Join(t.TempDir(), "starter.tgz")
		if err := TarGz("customer-platform", archive); err != nil {
			t.Fatal(err)
		}
		files := tarFiles(t, archive)
		if _, ok := files["customer-platform/test/billing.test.js"]; ok != released {
			t.Fatalf("released=%t: archive billing test present=%t", released, ok)
		}
		if strings.Contains(files["customer-platform/README.md"], "statement-finalize") != released {
			t.Fatalf("released=%t: archive README:\n%s", released, files["customer-platform/README.md"])
		}
	}
}

func tarFiles(t *testing.T, archive string) map[string]string {
	t.Helper()
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return files
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		files[hdr.Name] = string(body)
	}
}

func TestWithoutMonetizationBlocks(t *testing.T) {
	in := "a\n" + monetizationStart + "b\n" + monetizationEnd + "c\n" + monetizationStart + "d\n"
	if got := string(withoutMonetizationBlocks([]byte(in))); got != "a\nc\n" {
		t.Fatalf("got %q", got)
	}
}
