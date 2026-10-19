package objectnode

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/objectchecksum"
)

func TestCatalogKilledPublicationAndPartialExpiryRebuild(t *testing.T) {
	if boundary := os.Getenv("GREGALE_CATALOG_BOUNDARY"); boundary != "" {
		s, err := Open(os.Getenv("GREGALE_CATALOG_DIRECTORY"))
		if err != nil {
			t.Fatal(err)
		}
		checkpoint := func(phase string) error {
			if phase == boundary {
				_, _ = os.Stdout.WriteString("catalog boundary\n")
				select {}
			}
			return nil
		}
		s.catalogCheckpoint = checkpoint
		original := s.persist
		s.persist = func(path string, value any) (bool, error) {
			published, err := original(path, value)
			if err == nil && path == filepath.Join(s.objectPath("test-bucket", "fresh"), "current.json") {
				_ = checkpoint("source published")
			}
			return published, err
		}
		if boundary == "expiry part removed" {
			now := time.Now().Add(48 * time.Hour)
			_, err = s.ExpireMultipartStep(context.Background(), now, expiryTestOptions(1, 1))
		} else {
			_, err = s.Put(context.Background(), "test-bucket", "fresh", strings.NewReader("acknowledge source"), 18, PutOptions{})
		}
		t.Fatal("child missed boundary", boundary, err)
	}
	for _, replicated := range []bool{false, true} {
		for _, boundary := range []string{"source published", "index applied", "expiry part removed"} {
			t.Run(map[bool]string{false: "local/", true: "replicated/"}[replicated]+boundary, func(t *testing.T) {
				var s *Store
				if replicated {
					s, _ = replicationPair(t)
				} else {
					s = testStore(t)
				}
				putText(t, s, "survives", "current")
				u := newMultipart(t, s, objectchecksum.Policy{})
				multipartPart(t, s, u, 1, []byte("one"))
				multipartPart(t, s, u, 2, []byte("two"))
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
				defer cancel()
				command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCatalogKilledPublicationAndPartialExpiryRebuild$")
				command.Env = append(os.Environ(), "GREGALE_CATALOG_BOUNDARY="+boundary, "GREGALE_CATALOG_DIRECTORY="+s.root)
				output, err := command.StdoutPipe()
				if err != nil {
					t.Fatal(err)
				}
				command.Stderr = os.Stderr
				if err = command.Start(); err != nil {
					t.Fatal(err)
				}
				defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
				if scanner := bufio.NewScanner(output); !scanner.Scan() || scanner.Text() != "catalog boundary" {
					t.Fatal("child missed checkpoint", boundary)
				}
				if err = command.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				_ = command.Wait()
				next, err := Open(s.root)
				if err != nil {
					t.Fatal(err)
				}
				defer next.Close()
				if readText(t, next, "survives") != "current" {
					t.Fatal("current object lost")
				}
				if boundary == "expiry part removed" {
					got, err := next.readMultipart(u.ID)
					if err != nil || got.Status != multipartExpired {
						t.Fatal("terminal proof lost", got, err)
					}
					if _, err = os.Stat(filepath.Join(next.multipartPath(u.ID), "parts")); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("interrupted expiry failed to resume", err)
					}
				} else if page, err := next.List(t.Context(), "test-bucket", ListRequest{Prefix: "fresh", Limit: 1}); err != nil || len(page.Entries) != 1 || page.Entries[0].Key != "fresh" {
					t.Fatal("crash hid source publication", page, err)
				}
				assertUsageCountersMatchSource(t, next)
			})
		}
	}
}

func TestCatalogIndexBackupOmissionAndRestore(t *testing.T) {
	s := testStore(t)
	putText(t, s, "source", "safe")
	_ = s.Close()
	backup, restored := filepath.Join(t.TempDir(), "backup"), filepath.Join(t.TempDir(), "restored")
	if _, err := Backup(t.Context(), s.root, backup); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(backup, "data", catalogIndexName)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("backup exported derived database", err)
	}
	if _, err := RestoreBackup(t.Context(), backup, restored); err != nil {
		t.Fatal(err)
	}
	next, err := Open(restored)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if page, err := next.List(t.Context(), "test-bucket", ListRequest{Limit: 10}); err != nil || len(page.Entries) != 1 || readText(t, next, "source") != "safe" {
		t.Fatal("restored listing index incorrect", page, err)
	}
	assertUsageCountersMatchSource(t, next)
	if err = os.WriteFile(path, []byte("unlisted database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyBackup(t.Context(), backup); err == nil {
		t.Fatal("verify accepted unexpected derived file")
	}
}
