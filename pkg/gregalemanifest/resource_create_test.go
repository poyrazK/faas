package gregalemanifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadCreateManifest(t *testing.T, body string) (*Manifest, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m, _, err := Load(dir)
	if err != nil {
		return nil, err
	}
	return m, m.Validate()
}

func TestResourceCreateParsesTrueAndObjects(t *testing.T) {
	m, err := loadCreateManifest(t, `databases:
  - database: chronolake-db
    app: gateway
    create:
      postgres_major: 17
      class: development
      scale_to_zero: true
  - database: analytics
    create: true
buckets:
  - bucket: segments
    create:
      region: us-east-1
  - bucket: exports
    create: true
  - bucket: existing
`)
	if err != nil {
		t.Fatal(err)
	}
	first := m.Databases[0].Create
	if first == nil || first.PostgresMajor != 17 || first.Class != "development" || first.ScaleToZero == nil || !*first.ScaleToZero {
		t.Fatalf("database create = %+v", first)
	}
	request := first.Request(m.Databases[0].Database)
	if request.Name != "chronolake-db" || request.PostgresMajor != 17 || request.ServiceClass != "development" {
		t.Fatalf("create request = %+v", request)
	}
	if m.Databases[1].Create == nil || *m.Databases[1].Create != (DatabaseCreate{}) {
		t.Fatalf("create: true = %+v", m.Databases[1].Create)
	}
	if m.Buckets[0].Create == nil || m.Buckets[0].Create.Region != "us-east-1" || m.Buckets[1].Create == nil || m.Buckets[2].Create != nil {
		t.Fatalf("bucket creates = %+v %+v %+v", m.Buckets[0].Create, m.Buckets[1].Create, m.Buckets[2].Create)
	}
}

func TestResourceCreateRejectsBadShapes(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"false", "databases:\n  - database: a\n    create: false\n", "must be true or an object"},
		{"unknown key", "databases:\n  - database: a\n    create:\n      size: big\n", `unknown key "size"`},
		{"bucket unknown key", "buckets:\n  - bucket: a\n    create:\n      public: true\n", `unknown key "public"`},
		{"database id", "databases:\n  - database: 0b6c3f0e-7c2a-4a8e-9a55-1c0e2d7f4b11\n    create: true\n", "not an ID"},
		{"database name", "databases:\n  - database: Orders\n    create: true\n", "not an ID"},
		{"class", "databases:\n  - database: a\n    create:\n      class: huge\n", "class"},
		{"major", "databases:\n  - database: a\n    create:\n      postgres_major: 9\n", "postgres_major"},
		{"create twice", "databases:\n  - database: a\n    app: x\n    create: true\n  - database: a\n    app: y\n    create: true\n", "once per database"},
		{"bucket name", "buckets:\n  - bucket: Segments_1\n    create: true\n", "bucket must be a name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadCreateManifest(t, tc.body)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}
