package main

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCredentialRe(t *testing.T) {
	for _, tc := range []struct {
		line string
		want string
	}{
		{"LoadCredential=faas_fleet_age_identity:/etc/faas/secrets/fleet.age", "/etc/faas/secrets/fleet.age"},
		{"LoadCredential=prev:/etc/faas/secrets/fleet.age.previous\n", "/etc/faas/secrets/fleet.age.previous"},
		{`          LoadCredential=x:/etc/faas/secrets/domains/outbound.age "`, "/etc/faas/secrets/domains/outbound.age"},
		{"LoadCredential=faas_fleet_age_recipient:/etc/faas/secrets/fleet.age.pub", ""},
		{"LoadCredential=faas_host_hmac_key:/etc/faas/secrets/host.hmac.key", ""},
		{"# LoadCredential=a:/srv/other.age", ""},
	} {
		m := credentialRe.FindStringSubmatch(tc.line)
		got := ""
		if m != nil {
			got = m[1]
		}
		if got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.line, got, tc.want)
		}
	}
}

func TestOwnIdentity(t *testing.T) {
	for _, tc := range []struct {
		daemon, path string
		want         bool
	}{
		{"outboundd", "/etc/faas/secrets/domains/outbound.age", true},
		{"outboundd", "/etc/faas/secrets/domains/outbound.age.previous", true},
		{"apid", "/etc/faas/secrets/domains/outbound.age", false},
		{"maild", "/etc/faas/secrets/internal-mail/mail.age", true},
		{"outboundd", "/etc/faas/secrets/fleet.age", false},
	} {
		if got := ownIdentity(tc.daemon, tc.path); got != tc.want {
			t.Errorf("ownIdentity(%s, %s) = %v, want %v", tc.daemon, tc.path, got, tc.want)
		}
	}
	if d := sealDomainReaderTemplate("deploy/ansible/roles/seal_domains/templates/60-faas-seal-domain-outbound-reader.conf.j2"); d != "outbound" {
		t.Errorf("reader template domain = %q", d)
	}
	if d := sealDomainReaderTemplate("deploy/ansible/roles/seal_domains/templates/60-faas-seal-domain-outbound-writer.conf.j2"); d != "" {
		t.Errorf("a writer template counted as a reader: %q", d)
	}
}

func TestCountLoaderCalls(t *testing.T) {
	src := `package x
// secretbox.LoadHostKey in a comment does not count.
func f() {
	_, _ = secretbox.LoadHostKey("a")
	load := secretbox.LoadFleetAndHostKeys
	_, _ = age.ParseX25519Identity("k")
	_, _ = sealdomain.LoadReaderIdentities("a")
	_ = load
}`
	f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	if n := countLoaderCalls(f); n != 3 {
		t.Fatalf("countLoaderCalls = %d, want 3 (call, function value, age parse)", n)
	}
}

func TestScanPlainColumns(t *testing.T) {
	schema := filepath.Join(t.TempDir(), "schema.sql")
	body := `CREATE TABLE public.app_secrets (
    ciphertext bytea NOT NULL,
    kid text
);
CREATE TABLE public.github_webhook_secrets (
    secret_value bytea NOT NULL,
    secret_sealed bytea
);
CREATE TABLE public.new_feature_tokens (
    token_sealed bytea NOT NULL
);
`
	if err := os.WriteFile(schema, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := scanPlainColumns(schema)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "new_feature_tokens.token_sealed" {
		t.Fatalf("unowned columns = %v; want only the unregistered one", got)
	}
}

func TestCompare(t *testing.T) {
	baseline := map[string]bool{"loader cmd/a/main.go 2": true, "credential unit:faas-x /etc/faas/secrets/fleet.age": true}
	found := map[string]bool{"loader cmd/a/main.go 1": true, "plain t.c": true}
	problems := compare(found, baseline)
	joined := strings.Join(problems, "\n")
	for _, want := range []string{
		"new: loader cmd/a/main.go 1 — fewer loaders than the baseline; lower it",
		"new: plain t.c",
		"stale baseline entry (remove it, or run -write): credential unit:faas-x /etc/faas/secrets/fleet.age",
		"stale baseline entry (remove it, or run -write): loader cmd/a/main.go 2",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in:\n%s", want, joined)
		}
	}
	if len(compare(baseline, baseline)) != 0 {
		t.Error("an exact baseline reported problems")
	}
}
