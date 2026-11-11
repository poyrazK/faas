package apidserver_test

// adr: 20261110120000 — a first deploy whose gregale.yaml declares the
// database or bucket an after_restore hook reconnects to counts as bound.

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

func TestManifestDeclaresManagedBinding(t *testing.T) {
	for _, tc := range []struct {
		name     string
		manifest *gregalemanifest.Manifest
		want     bool
	}{
		{name: "no manifest"},
		{name: "no dependencies", manifest: &gregalemanifest.Manifest{}},
		{name: "database for the deployed app", manifest: &gregalemanifest.Manifest{Databases: []gregalemanifest.DatabaseDependency{{Database: "orders"}}}, want: true},
		{name: "database named for the app", manifest: &gregalemanifest.Manifest{Databases: []gregalemanifest.DatabaseDependency{{Database: "orders", App: "API"}}}, want: true},
		{name: "database for another app", manifest: &gregalemanifest.Manifest{Databases: []gregalemanifest.DatabaseDependency{{Database: "orders", App: "worker"}}}},
		{name: "bucket for the deployed app", manifest: &gregalemanifest.Manifest{Buckets: []gregalemanifest.BucketDependency{{Bucket: "uploads"}}}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := apidserver.ManifestDeclaresManagedBinding(tc.manifest, "api"); got != tc.want {
				t.Fatalf("ManifestDeclaresManagedBinding = %t, want %t", got, tc.want)
			}
		})
	}
}
