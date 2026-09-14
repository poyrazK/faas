package logarchive

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive-creds.json")
	if err := os.WriteFile(path, []byte(`{"endpoint":"https://objects.example","region":"eu-1","bucket":"logs","key_id":"id","secret":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := ReadCredentials(path)
	if err != nil {
		t.Fatalf("ReadCredentials: %v", err)
	}
	if got != (Credentials{
		Endpoint: "https://objects.example",
		Region:   "eu-1",
		Bucket:   "logs",
		KeyID:    "id",
		Secret:   "secret",
	}) {
		t.Fatalf("credentials = %#v", got)
	}
}

func TestReadCredentialsMissingFile(t *testing.T) {
	_, err := ReadCredentials(filepath.Join(t.TempDir(), "missing.json"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want os.ErrNotExist", err)
	}
}

func TestConfigWithCredentialsPreservesExplicitValues(t *testing.T) {
	c := Config{
		Endpoint:       "https://env.example",
		Region:         "us-east-1",
		Bucket:         "env-bucket",
		KeyID:          "env-id",
		Secret:         "env-secret",
		regionExplicit: true,
	}
	got := c.WithCredentials(Credentials{
		Endpoint: "https://file.example",
		Region:   "file-region",
		Bucket:   "file-bucket",
		KeyID:    "file-id",
		Secret:   "file-secret",
	})
	if got != c {
		t.Fatalf("config changed despite explicit values: %#v", got)
	}
}

func TestConfigWithCredentialsFillsUnsetValues(t *testing.T) {
	got := (Config{}).WithCredentials(Credentials{
		Endpoint: "https://file.example",
		Region:   "file-region",
		Bucket:   "file-bucket",
		KeyID:    "file-id",
		Secret:   "file-secret",
	})
	if got.Endpoint != "https://file.example" || got.Region != "file-region" || got.Bucket != "file-bucket" || got.KeyID != "file-id" || got.Secret != "file-secret" {
		t.Fatalf("config = %#v", got)
	}
}

func TestConfigWithCredentialsSelectsGCPMetadataAuth(t *testing.T) {
	got := (Config{}).WithCredentials(Credentials{
		Endpoint: "https://storage.googleapis.com", Region: "auto", Bucket: "logs", AuthMode: "gcp_metadata",
	})
	if got.AuthMode != "gcp_metadata" || got.KeyID != "" || got.Secret != "" {
		t.Fatalf("config = %#v, want keyless GCP metadata auth", got)
	}
}

func TestConfigWithCredentialsPreservesDirectRegion(t *testing.T) {
	c := Config{Region: "eu-direct"}
	got := c.WithCredentials(Credentials{Region: "file-region"})
	if got.Region != c.Region {
		t.Fatalf("region = %q, want direct value %q", got.Region, c.Region)
	}
}
