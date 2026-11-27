package main

// adr: 20261117190000

import (
	"os"
	"path/filepath"
	"testing"
)

// Storage grants are off unless a store is named, and a named store needs
// its bucket and credentials.
func TestStorageGrantsFromEnv(t *testing.T) {
	secretFile := filepath.Join(t.TempDir(), "s3-secret")
	if err := os.WriteFile(secretFile, []byte("s3cr3t\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s3env := map[string]string{"FAAS_SECRETD_STORAGE_GRANTS": "s3", "FAAS_SECRETD_STORAGE_BUCKET": "artifacts",
		"FAAS_SECRETD_STORAGE_S3_ENDPOINT": "https://s3.example.test", "FAAS_SECRETD_STORAGE_S3_REGION": "us-east-1",
		"FAAS_SECRETD_STORAGE_S3_ACCESS_KEY": "AKID", "FAAS_SECRETD_STORAGE_S3_SECRET_KEY_FILE": secretFile}
	for name, tc := range map[string]struct {
		env     map[string]string
		backend string
		ok      bool
	}{
		"default off":         {env: map[string]string{}, backend: "off", ok: true},
		"gcs":                 {env: map[string]string{"FAAS_SECRETD_STORAGE_GRANTS": "gcs", "FAAS_SECRETD_STORAGE_BUCKET": "artifacts"}, backend: "gcs", ok: true},
		"gcs without bucket":  {env: map[string]string{"FAAS_SECRETD_STORAGE_GRANTS": "gcs"}},
		"s3":                  {env: s3env, backend: "s3", ok: true},
		"s3 without a secret": {env: map[string]string{"FAAS_SECRETD_STORAGE_GRANTS": "s3", "FAAS_SECRETD_STORAGE_BUCKET": "artifacts"}},
		"unknown":             {env: map[string]string{"FAAS_SECRETD_STORAGE_GRANTS": "azure"}},
	} {
		c, err := storageGrantsFromEnv(func(k string) string { return tc.env[k] })
		if (err == nil) != tc.ok || (tc.ok && c.backend != tc.backend) {
			t.Errorf("%s: backend %q, err %v; want %q ok=%v", name, c.backend, err, tc.backend, tc.ok)
		}
		if name == "s3" && c.secret != "s3cr3t" {
			t.Errorf("s3 secret = %q, want the trimmed file contents", c.secret)
		}
	}
	off, _ := storageGrantsFromEnv(func(string) string { return "" })
	if s, _, err := off.signer(t.Context()); s != nil || err != nil {
		t.Fatalf("off signer = %v, %v", s, err)
	}
	s3c, _ := storageGrantsFromEnv(func(k string) string { return s3env[k] })
	if s, _, err := s3c.signer(t.Context()); s == nil || err != nil {
		t.Fatalf("s3 signer = %v, %v", s, err)
	}
}
