package journeys

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// GA inventory section H (account) and I (developer tools) journeys, plus
// the custom-domain binding flow without DNS (row 63).

// TestBillingAndUsage reads every billing and usage view the CLI offers
// for the account (row 151). It changes nothing: plan, payment and
// overage-cap changes would bill the account.
func TestBillingAndUsage(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	today := time.Now().UTC().Format("2006-01-02")
	views := map[string][]string{
		"billing_status":   {"billing", "status"},
		"billing_costs":    {"billing", "costs"},
		"billing_forecast": {"billing", "forecast"},
		"usage_month":      {"usage"},
		"usage_daily":      {"usage", "daily", "--day", today},
		"usage_summary":    {"usage", "summary"},
		"invoices":         {"invoices"},
	}
	for name, args := range views {
		t.Run(name, func(t *testing.T) {
			res := tg.ok(t, time.Minute, args...)
			objects(t, res.stdout)
		})
	}
}

// TestAccountExportAndDPA requests the account's GDPR data export (without
// sealed-secret ciphertext), and reads the account status and DPA (row 153).
// Account deletion is not run: it needs a disposable account.
func TestAccountExportAndDPA(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	t.Run("export", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "export")
		tg.ok(t, 5*time.Minute, "account", "export", "-o", out, "--no-secrets")
		info, err := os.Stat(out)
		if err != nil || info.Size() == 0 {
			t.Fatalf("account export wrote no data to %s: %v", out, err)
		}
		t.Logf("export: %d bytes", info.Size())
	})
	t.Run("status", func(t *testing.T) {
		objects(t, tg.ok(t, time.Minute, "account", "status").stdout)
	})
	t.Run("dpa", func(t *testing.T) {
		res := tg.ok(t, time.Minute, "account", "dpa")
		if !strings.Contains(strings.ToLower(string(res.stdout)), "processing") {
			t.Fatalf("account dpa does not print a data processing agreement: %s", tail(string(res.stdout), 300))
		}
	})
}

// TestCLITools exercises the developer-facing CLI commands that need no
// app: version, context, local source doctor, shell completion, man pages
// and full help (row 156).
func TestCLITools(t *testing.T) {
	tg := needs(t, tierSmoke, true)
	t.Parallel()
	t.Run("version", func(t *testing.T) {
		if res := tg.ok(t, time.Minute, "version"); !strings.Contains(string(res.stdout), "v0.") {
			t.Fatalf("gregale version: %s", tail(string(res.stdout), 200))
		}
	})
	t.Run("context", func(t *testing.T) {
		tg.ok(t, time.Minute, "context")
	})
	t.Run("doctor_source", func(t *testing.T) {
		tg.ok(t, 2*time.Minute, "doctor", "--path", probeSource(t, "v1"))
	})
	t.Run("completion", func(t *testing.T) {
		res := tg.ok(t, time.Minute, "completion", "zsh")
		if !strings.Contains(string(res.stdout), "gregale") {
			t.Fatalf("zsh completion does not mention gregale: %s", tail(string(res.stdout), 200))
		}
	})
	t.Run("man", func(t *testing.T) {
		if res := tg.ok(t, time.Minute, "man", "deploy"); len(res.stdout) < 200 {
			t.Fatalf("man deploy is %d bytes", len(res.stdout))
		}
	})
	t.Run("help_all", func(t *testing.T) {
		res := tg.ok(t, time.Minute, "help", "--all")
		for _, cmd := range []string{"deploy", "logs", "rollback", "secrets", "domains"} {
			if !strings.Contains(string(res.stdout), cmd) {
				t.Errorf("help --all does not list %s", cmd)
			}
		}
	})
}

// TestCustomDomainBinding binds a custom domain the suite does not own,
// checks it reports pending verification with the DNS records to create,
// and removes it. Without a DNS zone it cannot check TLS issuance or
// serving (row 63).
func TestCustomDomainBinding(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	slug, _ := tg.deployProbe(t, "domain")
	domain := slug + ".example.com"
	t.Cleanup(func() { tg.run(t, time.Minute, "domains", "rm", domain) })
	s := newStory(t)

	s.step("add", func(t *testing.T) {
		res := tg.ok(t, time.Minute, "domains", "add", "--app", slug, domain)
		t.Logf("domains add: %s", tail(string(res.stdout), 500))
	})
	s.check("pending_with_records", func(t *testing.T) {
		res := tg.ok(t, time.Minute, "domains", "show", domain)
		out := strings.ToLower(string(res.stdout))
		if !strings.Contains(out, "pending") && !strings.Contains(out, "unverified") && !strings.Contains(out, "verif") {
			t.Errorf("domains show does not report pending verification: %s", tail(string(res.stdout), 400))
		}
		if !strings.Contains(out, "cname") && !strings.Contains(out, "txt") && !strings.Contains(out, "record") {
			t.Errorf("domains show names no DNS record to create: %s", tail(string(res.stdout), 400))
		}
	})
	s.check("verify_fails_without_dns", func(t *testing.T) {
		res := tg.run(t, time.Minute, "domains", "verify", domain)
		if res.exit == 0 && strings.Contains(strings.ToLower(string(res.stdout)), `"verified": true`) {
			t.Errorf("a domain with no DNS records verified")
		}
	})
	s.check("listed_then_removed", func(t *testing.T) {
		if res := tg.ok(t, time.Minute, "domains", "list"); !strings.Contains(string(res.stdout), domain) {
			t.Errorf("domains list does not show %s", domain)
		}
		tg.ok(t, time.Minute, "domains", "rm", domain)
		if res := tg.ok(t, time.Minute, "domains", "list"); strings.Contains(string(res.stdout), domain) {
			t.Errorf("%s is still listed after domains rm", domain)
		}
	})
}
