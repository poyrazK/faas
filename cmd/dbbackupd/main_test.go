// adr: 1027
package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/dbarchive"
)

const testDB = "5e2f7a9c-3b4d-4e6f-8a1b-2c3d4e5f6a7b"

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dbbackupd.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	secret := func(name, mode string) string {
		path := filepath.Join(dir, name)
		perm := os.FileMode(0o600)
		if mode == "shared" {
			perm = 0o644
		}
		if err := os.WriteFile(path, []byte("value\n"), perm); err != nil {
			t.Fatal(err)
		}
		return path
	}
	key, shared := secret("key", ""), secret("shared", "shared")
	cfg, err := LoadConfig(writeConfig(t, ""))
	if err != nil || cfg.Remote != nil || cfg.OpsAddr != DefaultOpsAddr || cfg.StoreDir != "/srv/db/archive" || cfg.MinRetention != 24*time.Hour || cfg.PruneInterval != time.Hour {
		t.Fatalf("defaults = %+v, %v", cfg, err)
	}
	remote := `[remote]
bucket = "b"
prefix = "native"
access_key_id_path = "` + key + `"
secret_access_key_path = "` + key + `"
`
	cfg, err = LoadConfig(writeConfig(t, remote))
	if err != nil || cfg.Remote.UploadInterval != 10*time.Second {
		t.Fatalf("remote = %+v, %v", cfg, err)
	}
	if s3, err := cfg.Remote.s3Config(); err != nil || s3.KeyID != "value" || s3.Secret != "value" {
		t.Fatalf("credentials = %+v, %v", s3, err)
	}
	offsite := strings.Replace(strings.Replace(remote, "[remote]", "[offsite]", 1), `bucket = "b"`, `bucket = "dr"`, 1)
	cfg, err = LoadConfig(writeConfig(t, remote+offsite))
	if err != nil || cfg.Offsite == nil || cfg.Offsite.Bucket != "dr" || cfg.Offsite.UploadInterval != time.Minute || cfg.Remote.UploadInterval != 10*time.Second {
		t.Fatalf("offsite = %+v, %v", cfg, err)
	}
	cfg.Remote.SecretAccessKeyPath = shared
	if _, err := cfg.Remote.s3Config(); err == nil {
		t.Fatal("a group-readable secret was accepted")
	}
	for name, body := range map[string]string{
		"relative store":      `store_dir = "archive"`,
		"tiny retention":      `min_retention = "10m"`,
		"fast pruning":        `prune_interval = "1s"`,
		"no prefix":           strings.Replace(remote, `prefix = "native"`, `prefix = "/"`, 1),
		"no bucket":           strings.Replace(remote, `bucket = "b"`, "", 1),
		"fast uploads":        remote + "upload_interval = \"10ms\"\n",
		"offsite alone":       strings.Replace(remote, "[remote]", "[offsite]", 1),
		"offsite same bucket": remote + strings.Replace(remote, "[remote]", "[offsite]", 1),
	} {
		if _, err := LoadConfig(writeConfig(t, body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// TestRunStoresRelayedWAL drives the daemon as vmmd's relay does: a header
// naming the database, then the guest's archive_command stream.
func TestRunStoresRelayedWAL(t *testing.T) {
	dir, err := os.MkdirTemp("", "dbb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock, store := filepath.Join(dir, "run", "d.sock"), filepath.Join(dir, "store")
	ops := freeAddr(t)
	path := writeConfig(t, `socket_path = "`+sock+`"
store_dir = "`+store+`"
ops_addr = "`+ops+`"
`)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, path, slog.New(slog.DiscardHandler)) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("run = %v", err)
		}
	})
	var conn net.Conn
	deadline := time.Now().Add(5 * time.Second)
	for conn == nil {
		if conn, err = net.Dial("unix", sock); err != nil {
			if time.Now().After(deadline) {
				t.Fatal(err)
			}
			conn = nil
			time.Sleep(10 * time.Millisecond)
		}
	}
	defer func() { _ = conn.Close() }()
	if info, err := os.Stat(sock); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %v, %v", info.Mode(), err)
	}
	if err := dbarchive.WriteLine(conn, dbarchive.Header{DatabaseID: testDB}); err != nil {
		t.Fatal(err)
	}
	const seg = "000000010000000000000001"
	if err := dbarchive.NewClient(conn).PutWAL(seg, strings.NewReader("wal"), 3); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(filepath.Join(store, testDB, "wal", seg)); err != nil || string(body) != "wal" {
		t.Fatalf("stored = %q, %v", body, err)
	}
	// Deleting the database drops its archive through vmmd's admin header.
	if err := dbarchive.DropArchive(context.Background(), sock, testDB); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(store, testDB)); !os.IsNotExist(err) {
		t.Fatalf("archive after drop: %v", err)
	}
	resp, err := http.Get("http://" + ops + "/readyz")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("readyz = %v, %v", resp, err)
	}
	_ = resp.Body.Close()
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}
