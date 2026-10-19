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

func TestBackupAndRestoreKilledAtPublication(t *testing.T) {
	if operation := os.Getenv("GREGALE_BACKUP_CRASH_OPERATION"); operation != "" {
		source, destination, boundary := os.Getenv("GREGALE_BACKUP_CRASH_SOURCE"), os.Getenv("GREGALE_BACKUP_CRASH_DESTINATION"), os.Getenv("GREGALE_BACKUP_CRASH_BOUNDARY")
		checkpoint := func(phase string) error {
			if phase == boundary {
				_, _ = os.Stdout.WriteString("recovery boundary reached\n")
				select {}
			}
			return nil
		}
		var err error
		if operation == "backup" {
			_, err = createBackup(context.Background(), source, destination, checkpoint)
		} else {
			_, err = restoreBackup(context.Background(), source, destination, checkpoint)
		}
		t.Fatal("child did not stop at the requested boundary", err)
	}
	for _, operation := range []string{"backup", "restore"} {
		for _, boundary := range []string{"file", "ready", "published"} {
			t.Run(operation+"/"+boundary, func(t *testing.T) {
				s, backup := backupFixture(t)
				source := s.root
				if operation == "restore" {
					source = backup
				}
				destination := filepath.Join(t.TempDir(), "final")
				ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
				defer cancel()
				command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBackupAndRestoreKilledAtPublication$")
				command.Env = append(os.Environ(), "GREGALE_BACKUP_CRASH_OPERATION="+operation, "GREGALE_BACKUP_CRASH_SOURCE="+source, "GREGALE_BACKUP_CRASH_DESTINATION="+destination, "GREGALE_BACKUP_CRASH_BOUNDARY="+boundary)
				out, err := command.StdoutPipe()
				if err != nil {
					t.Fatal(err)
				}
				command.Stderr = os.Stderr
				if err = command.Start(); err != nil {
					t.Fatal(err)
				}
				defer func() { _ = command.Process.Kill() }()
				if scanner := bufio.NewScanner(out); !scanner.Scan() || scanner.Text() != "recovery boundary reached" {
					t.Fatal("child did not reach boundary")
				}
				if err = command.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				_ = command.Wait()
				if boundary != "published" {
					if _, err = os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("interrupted operation published an incomplete directory", err)
					}
					// Retry at the same final destination. The crashed process's
					// unpublished sibling is never mistaken for a complete artifact.
					if operation == "backup" {
						_, err = Backup(t.Context(), source, destination)
					} else {
						_, err = RestoreBackup(t.Context(), source, destination)
					}
					if err != nil {
						t.Fatal("retry failed", err)
					}
				}
				if operation == "backup" {
					if _, err = VerifyBackup(t.Context(), destination); err != nil {
						t.Fatal(err)
					}
				} else {
					node, err := Open(destination)
					if err != nil {
						t.Fatal(err)
					}
					defer node.Close()
					if readText(t, node, "key") != "original" {
						t.Fatal("published restored node has incorrect bytes")
					}
				}
				if _, err = VerifyBackup(t.Context(), backup); err != nil {
					t.Fatal("crash changed original backup", err)
				}
			})
		}
	}
}

func TestBackupRestoreIOFailureAndDestinationRace(t *testing.T) {
	for _, operation := range []string{"backup", "restore"} {
		for _, failure := range []string{"write failure", "cancel after file", "destination race", "damaged copy"} {
			t.Run(operation+"/"+failure, func(t *testing.T) {
				s, backup := backupFixture(t)
				destination := filepath.Join(t.TempDir(), "final")
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				injected := errors.New("injected write or sync failure")
				var corrupted bool
				checkpoint := func(phase string) error {
					if phase == "file" && failure == "write failure" {
						return injected
					}
					if phase == "file" && failure == "cancel after file" {
						cancel()
					}
					if phase == "ready" && failure == "destination race" {
						if err := os.Mkdir(destination, 0o700); err != nil {
							return err
						}
						return os.WriteFile(filepath.Join(destination, "sentinel"), []byte("preserve"), 0o600)
					}
					if phase == "file" && failure == "damaged copy" && !corrupted {
						stages, err := filepath.Glob(filepath.Join(filepath.Dir(destination), ".object-"+operation+"-*"))
						if err != nil || len(stages) != 1 {
							t.Fatal(stages, err)
						}
						root := stages[0]
						if operation == "backup" {
							root = filepath.Join(root, "data")
						}
						// Alter a copied file after the copy path returned success.
						// Publication must wait for destination read-back verification.
						err = walkBackupTree(t.Context(), root, "", 0, func(path string, info os.FileInfo) error {
							if !info.IsDir() && strings.HasSuffix(path, ".data") && !corrupted {
								corrupted = true
								return os.WriteFile(filepath.Join(root, path), []byte("damaged!"), 0o600)
							}
							return nil
						})
						return err
					}
					return nil
				}
				var err error
				if operation == "backup" {
					_, err = createBackup(ctx, s.root, destination, checkpoint)
				} else {
					_, err = restoreBackup(ctx, backup, destination, checkpoint)
				}
				if err == nil || failure == "damaged copy" && !corrupted {
					t.Fatal("failure was not detected", err)
				}
				if failure == "destination race" {
					body, readErr := os.ReadFile(filepath.Join(destination, "sentinel"))
					if readErr != nil || string(body) != "preserve" {
						t.Fatal("existing destination overwritten", readErr)
					}
				} else if _, err = os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("failed operation published destination", err)
				}
				if _, err = VerifyBackup(t.Context(), backup); err != nil {
					t.Fatal("failed operation changed source", err)
				}
			})
		}
	}
}

func TestRestoreResumesFrozenCompletion(t *testing.T) {
	s := testStore(t)
	u := newMultipart(t, s, objectchecksum.Policy{})
	p := multipartPart(t, s, u, 1, []byte("frozen"))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if _, err := s.CompleteMultipart(ctx, u.Bucket, u.Key, u.ID, completion(p), func(int64) (func(), error) { cancel(); return func() {}, nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	backup, restored := filepath.Join(t.TempDir(), "backup"), filepath.Join(t.TempDir(), "restored")
	if _, err := Backup(t.Context(), s.root, backup); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreBackup(t.Context(), backup, restored); err != nil {
		t.Fatal(err)
	}
	node, err := Open(restored)
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	got, err := node.MultipartInfo(u.Bucket, u.Key, u.ID)
	if err != nil || got.Status != multipartCompleting || got.Completion == nil {
		t.Fatal(got, err)
	}
	if _, err = node.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, completion(p), nil); err != nil || readText(t, node, u.Key) != "frozen" {
		t.Fatal("frozen completion could not resume", err)
	}
}

func TestBackupSettlesMultipartIntent(t *testing.T) {
	s := testStore(t)
	u := newMultipart(t, s, objectchecksum.Policy{})
	p := multipartPart(t, s, u, 1, []byte("committed"))
	s.persist = func(path string, value any) (bool, error) {
		if path == filepath.Join(s.objectPath(u.Bucket, u.Key), "current.json") {
			return false, errors.New("interrupted multipart pointer")
		}
		return writeJSON(path, value)
	}
	if _, err := s.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, completion(p), nil); err == nil {
		t.Fatal("fault not reached")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	backup, restored := filepath.Join(t.TempDir(), "backup"), filepath.Join(t.TempDir(), "restored")
	if _, err := Backup(t.Context(), s.root, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(s.root); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreBackup(t.Context(), backup, restored); err != nil {
		t.Fatal(err)
	}
	node, err := Open(restored)
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	proof, err := node.MultipartReceipt(u.Bucket, u.Key, u.ReceiptID)
	if err != nil || proof.Status != ReceiptCommitted || readText(t, node, u.Key) != "committed" {
		t.Fatal("multipart intent lost in backup", proof, err)
	}
	if _, err = node.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, completion(p), nil); err != nil {
		t.Fatal("restored completion proof did not settle a retry", err)
	}
}
