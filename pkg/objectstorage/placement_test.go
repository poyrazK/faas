package objectstorage

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func poolBackend(id string, ceiling int64) BackendConfig {
	b := testBackend()
	b.ID, b.Namespace, b.MaxBuckets = id, "project-"+id, ceiling
	return b
}

// adr: 20261029174627
func TestRegistryPools(t *testing.T) {
	a, b := poolBackend("ovh-a", 950), poolBackend("ovh-b", 950)
	pooled := Config{DefaultRegion: "us-east-1", Pools: map[string][]string{"us-east-1": {"ovh-a", "ovh-b"}}, Backends: []BackendConfig{a, b}}
	r, err := NewRegistry(pooled, testCredentials, map[string]Factory{"s3": NewS3})
	if err != nil {
		t.Fatal(err)
	}
	placements, err := r.Placements("us-east-1")
	if err != nil || len(placements) != 2 || placements[0].ID != "ovh-a" || placements[1].ID != "ovh-b" || placements[1].MaxBuckets != 950 {
		t.Fatalf("placements = %+v, %v", placements, err)
	}
	if def, err := r.Default("us-east-1"); err != nil || def.ID != "ovh-a" || !r.PlacementLimited("us-east-1") {
		t.Fatalf("pool default = %+v, %v", def, err)
	}
	if placements[0].Fingerprint == placements[1].Fingerprint {
		t.Fatal("two provider projects share a placement fingerprint")
	}
	single := Config{DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "ovh-a"}, Backends: []BackendConfig{poolBackend("ovh-a", 0)}}
	if r, err = NewRegistry(single, testCredentials, map[string]Factory{"s3": NewS3}); err != nil || r.PlacementLimited("us-east-1") {
		t.Fatal("a lone default without a ceiling needs no placement check", err)
	}
	if got, _ := r.Placements("us-east-1"); len(got) != 1 || got[0].ID != "ovh-a" {
		t.Fatalf("default-only placements = %+v", got)
	}
	single.Backends[0].MaxBuckets = 10
	if r, err = NewRegistry(single, testCredentials, map[string]Factory{"s3": NewS3}); err != nil || !r.PlacementLimited("us-east-1") {
		t.Fatal("a lone default with a ceiling needs the placement check", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"empty pool", func(c *Config) { c.Pools["us-east-1"] = nil }, "is empty"},
		{"unknown backend", func(c *Config) { c.Pools["us-east-1"] = []string{"ovh-a", "ovh-c"} }, "missing, repeated or other-region"},
		{"repeated backend", func(c *Config) { c.Pools["us-east-1"] = []string{"ovh-a", "ovh-a"} }, "missing, repeated or other-region"},
		{"default is not the pool's first", func(c *Config) { c.Defaults = map[string]string{"us-east-1": "ovh-b"} }, "pool's first backend"},
		{"pool backend without a ceiling", func(c *Config) { c.Backends[1].MaxBuckets = 0 }, "needs max_buckets"},
		{"negative ceiling", func(c *Config) { c.Backends[0].MaxBuckets = -1 }, "invalid max_buckets"},
		{"ceiling above the bound", func(c *Config) { c.Backends[0].MaxBuckets = api.MaxObjectStorageBackendBuckets + 1 }, "invalid max_buckets"},
		{"other region", func(c *Config) { c.Backends[1].Region = "eu-west-1" }, "missing, repeated or other-region"},
		{"different Object Lock", func(c *Config) { c.Backends[1].ObjectLock.Enabled = true }, "same features"},
		{"different encryption", func(c *Config) { c.Backends[1].Encryption.Algorithms = []string{"AES256"} }, "same features"},
		{"different origins", func(c *Config) { c.Backends[1].AllowedOrigins = []string{"https://gregale.dev"} }, "same features"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := pooled
			c.Pools = map[string][]string{"us-east-1": {"ovh-a", "ovh-b"}}
			c.Backends = []BackendConfig{poolBackend("ovh-a", 950), poolBackend("ovh-b", 950)}
			tc.mutate(&c)
			if _, err := NewRegistry(c, testCredentials, map[string]Factory{"s3": NewS3}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("registry error = %v; want %q", err, tc.want)
			}
		})
	}
	same := pooled
	same.Defaults = map[string]string{"us-east-1": "ovh-a"}
	if _, err := NewRegistry(same, testCredentials, map[string]Factory{"s3": NewS3}); err != nil {
		t.Fatal("a default naming the pool's first backend was refused", err)
	}
}
