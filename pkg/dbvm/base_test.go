// adr: 1027
package dbvm

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestValidateImageRequiresADigest(t *testing.T) {
	ok := "docker.io/library/postgres@sha256:66aafa11cf15800a3c94763f7e11d1e7b2e37e5e84bc4ce027cb6e1e4bfaf4df"
	if err := ValidateImage(ok); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"postgres:17", "docker.io/library/postgres:17", "docker.io/library/postgres@sha256:abc", ok + " ", "docker.io/library/postgres:17@sha256:66aafa11cf15800a3c94763f7e11d1e7b2e37e5e84bc4ce027cb6e1e4bfaf4df"} {
		if err := ValidateImage(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestBasePathChangesWithImageOrGuestInit(t *testing.T) {
	a := BasePath("/b", 17, "img@1", "init-1")
	if a != BasePath("/b", 17, "img@1", "init-1") {
		t.Fatal("BasePath is not deterministic")
	}
	for _, other := range []string{BasePath("/b", 17, "img@2", "init-1"), BasePath("/b", 17, "img@1", "init-2"), BasePath("/b", 16, "img@1", "init-1")} {
		if other == a {
			t.Fatalf("%s collides with %s", other, a)
		}
	}
}

func TestPruneBasesKeepsCurrentAndOtherMajors(t *testing.T) {
	dir := t.TempDir()
	keep := BasePath(dir, 17, "img", "new")
	old := BasePath(dir, 17, "img", "old")
	other := BasePath(dir, 16, "img", "new")
	for _, p := range []string{keep, old, old + ".building", other} {
		if err := os.WriteFile(p, nil, 0o444); err != nil {
			t.Fatal(err)
		}
	}
	if err := PruneBases(dir, 17, keep); err != nil {
		t.Fatal(err)
	}
	left, _ := filepath.Glob(filepath.Join(dir, "*"))
	if len(left) != 2 {
		t.Fatalf("left %v, want the current 17 base and the 16 base", left)
	}
	for _, p := range []string{keep, other} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s removed", p)
		}
	}
}

// The Ansible role pins the same images the release qualifies.
func TestPostgresImagesMatchTheAnsibleDefaults(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "roles", "database_vmmd", "defaults", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var defaults struct {
		Images map[string]string `yaml:"database_vmmd_postgres_images"`
	}
	if err := yaml.Unmarshal(raw, &defaults); err != nil {
		t.Fatal(err)
	}
	if len(defaults.Images) != len(PostgresImages) {
		t.Fatalf("ansible pins %d majors, the release %d", len(defaults.Images), len(PostgresImages))
	}
	for major, image := range PostgresImages {
		if err := ValidateImage(image); err != nil {
			t.Errorf("major %d: %v", major, err)
		}
		if got := defaults.Images[strconv.Itoa(major)]; got != image {
			t.Errorf("major %d: ansible pins %q, the release %q", major, got, image)
		}
	}
}
