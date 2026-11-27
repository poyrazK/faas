package main_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// boa multi-service deploy (2026-11-21): a PEM key pasted as a quoted
// multiline dotenv value was refused at its second line.
func TestReadSecretsFileMultilineQuotedValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.env")
	body := strings.Join([]string{
		"# keys",
		`JWT_PRIVATE_KEY="-----BEGIN RSA PRIVATE KEY-----`,
		"MIIEowIBAAKCAQEA",
		"-----END RSA PRIVATE KEY-----\"",
		"export SINGLE='line one",
		"line two'",
		`ESCAPED="a\"`,
		`b"`,
		`INLINE="x\ny"`,
		"PLAIN=value",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	pairs, err := main.ReadSecretsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, pair := range pairs {
		got[pair.Key] = pair.Value
	}
	want := map[string]string{
		"JWT_PRIVATE_KEY": "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\n-----END RSA PRIVATE KEY-----",
		"SINGLE":          "line one\nline two",
		"ESCAPED":         "a\"\nb",
		"INLINE":          "x\ny",
		"PLAIN":           "value",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pairs = %q, want %q", got, want)
	}
}

func TestReadSecretsFileUnclosedQuote(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.env")
	if err := os.WriteFile(path, []byte("A=1\nKEY=\"-----BEGIN\nabc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := main.ReadSecretsFile(path)
	if err == nil || !strings.Contains(err.Error(), "line 2") || strings.Contains(err.Error(), "abc") {
		t.Fatalf("err = %v, want a line-2 unclosed-quote error without the value", err)
	}
}

// boa multi-service deploy (2026-11-21): `deploy --project --only contacts
// --secrets-file s.env` set userservice's private key on contacts. The plan
// lists only the selected service, so the owners come from the local scan.
func TestProjectDeployInitialSecretsScopesKeysToOwners(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"compose.yaml": "services:\n" +
			"  contacts:\n    build: ./contacts\n    ports: [\"8080:8080\"]\n    environment:\n      JWT_PUBLIC_KEY: \"\"\n      SHARED: \"\"\n" +
			"  userservice:\n    build: ./userservice\n    environment:\n      JWT_PRIVATE_KEY: \"\"\n      SHARED: \"\"\n",
		"contacts/requirements.txt":    "flask\n",
		"contacts/app.py":              "print(1)\n",
		"userservice/requirements.txt": "flask\n",
		"userservice/app.py":           "print(1)\n",
	}
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	selected := []api.PlanWorkload{{Name: "contacts", EnvKeys: []string{"JWT_PUBLIC_KEY", "SHARED"}}}
	pairs := []main.SecretsPair{
		{Key: "JWT_PUBLIC_KEY", Value: "pub"},
		{Key: "JWT_PRIVATE_KEY", Value: "priv"},
		{Key: "SHARED", Value: "s"},
		{Key: "UNDECLARED", Value: "u"},
	}
	owners := main.ProjectSecretOwnersFor(dir, selected)
	if !owners["JWT_PRIVATE_KEY"]["userservice"] {
		t.Fatalf("local scan did not find userservice's keys: %v", owners)
	}
	got := main.ProjectDeployInitialSecrets(selected, pairs, owners)
	want := api.ProjectInitialSecrets{"contacts": {"JWT_PUBLIC_KEY": "pub", "SHARED": "s", "UNDECLARED": "u"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("initial secrets = %v, want %v", got, want)
	}
	// Without owners (no scan), every key still goes to every selected service.
	if got := main.ProjectDeployInitialSecrets(selected, pairs, nil); len(got["contacts"]) != len(pairs) {
		t.Fatalf("nil owners = %v, want all keys", got)
	}
}
