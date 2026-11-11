package main_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// adr: 20261107210000
// Offline stand-ins publish only on loopback, keep data in per-project
// volumes and run pinned images.
func TestDevLocalOfflineContainers(t *testing.T) {
	pg := main.OfflinePostgresRunArgsForTest("/work/shop")
	s3 := main.OfflineS3RunArgsForTest("/work/shop")
	for name, args := range map[string][]string{"postgres": pg, "s3": s3} {
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "-p 127.0.0.1::") {
			t.Errorf("%s publishes beyond loopback: %s", name, joined)
		}
		if !strings.Contains(joined, "--rm") || !strings.Contains(joined, "gregale-dev-shop-") {
			t.Errorf("%s args = %s", name, joined)
		}
	}
	if !slices.Contains(pg, "postgres:16") || !slices.Contains(s3, "versity/versitygw:v1.10.0") {
		t.Errorf("images: %v / %v", pg, s3)
	}
	if other := main.OfflinePostgresRunArgsForTest("/elsewhere/shop"); reflect.DeepEqual(other, pg) {
		t.Error("two projects with the same directory name share a container and volume")
	}
	if got := main.OfflinePostgresURLForTest("55432"); got != "postgresql://gregale:gregale-local@127.0.0.1:55432/gregale?sslmode=disable" {
		t.Errorf("url = %s", got)
	}
	env := main.OfflineBucketEnvForTest("uploads", "http://127.0.0.1:7070")
	if env["GREGALE_S3_UPLOADS_ENDPOINT"] != "http://127.0.0.1:7070" || env["GREGALE_S3_UPLOADS_BUCKET"] != "uploads" ||
		env["GREGALE_S3_UPLOADS_ADDRESSING_STYLE"] != "path" || len(env) != 6 {
		t.Errorf("bucket env = %v", env)
	}
}

func TestDevLocalOfflineOptions(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte("dev:\n  postgres: true\n  buckets:\n    - name: assets\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pg, buckets, err := main.OfflineOptionsForTest(dir, false, []string{"uploads", "assets"})
	if err != nil || !pg || !reflect.DeepEqual(buckets, []string{"uploads", "assets"}) {
		t.Fatalf("options = %v %v %v, want postgres from dev: and deduplicated buckets", pg, buckets, err)
	}
	if _, _, err := main.OfflineOptionsForTest(t.TempDir(), false, []string{"Bad_Name"}); err == nil {
		t.Error("an invalid bucket name was accepted")
	}
}

func TestDevLocalOfflineFlagsNeedOffline(t *testing.T) {
	var stderr bytes.Buffer
	old := *main.OsStderr
	*main.OsStderr = &stderr
	defer func() { *main.OsStderr = old }()
	if code := main.Run([]string{"dev", "--local", "--postgres", "--path", t.TempDir()}); code == 0 {
		t.Fatal("--postgres without --offline was accepted")
	}
	if !strings.Contains(stderr.String(), "--offline") {
		t.Errorf("stderr = %q", stderr.String())
	}
}
