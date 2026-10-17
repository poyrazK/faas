package objectnode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
)

func backupFixture(t *testing.T) (*Store, string) {
	t.Helper()
	s := testStore(t)
	putText(t, s, "key", "original")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "backup")
	if _, err := Backup(t.Context(), s.root, path); err != nil {
		t.Fatal(err)
	}
	return s, path
}

func TestBackupRestoreAfterSourceLoss(t *testing.T) {
	s := testStore(t)
	// Historical proofs survive even when their bucket and object no longer do.
	committed, rejected := uuid.NewString(), uuid.NewString()
	original, err := receiptPut(t, s, "historical", "acknowledged", committed, PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = receiptPut(t, s, "historical", "rejected", rejected, PutOptions{IfNoneMatch: "*"}); !errors.Is(err, ErrPrecondition) {
		t.Fatal(err)
	}
	if err = s.Delete("test-bucket", "historical"); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	putText(t, s, "../../literal +key.txt", "durable data")
	putText(t, s, "empty", "")
	if err = s.CreateBucket("empty-bucket"); err != nil {
		t.Fatal(err)
	}
	u := newMultipart(t, s, objectchecksum.Policy{})
	first := multipartPart(t, s, u, 1, bytes.Repeat([]byte("a"), 5<<20))
	completedUpload := newMultipart(t, s, objectchecksum.Policy{})
	completedPart := multipartPart(t, s, completedUpload, 1, []byte("complete"))
	completed, err := s.CompleteMultipart(t.Context(), completedUpload.Bucket, completedUpload.Key, completedUpload.ID, completion(completedPart), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Delete("test-bucket", completedUpload.Key); err != nil {
		t.Fatal(err)
	}
	// Record a real finding, then supersede its damaged identity. A good backup
	// retains the diagnostic without copying the obsolete damaged blob.
	putText(t, s, "damaged", "original")
	if err = os.WriteFile(scrubObjectPath(t, s, "damaged"), []byte("damaged!"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartScrub(t.Context()); err != nil {
		t.Fatal(err)
	}
	finishTestScrub(t, s, 64<<20)
	putText(t, s, "damaged", "replacement")
	findings, err := s.ScrubFindings(t.Context(), "", 10)
	if err != nil || len(findings.Items) != 1 {
		t.Fatal(findings, err)
	}
	if _, err = s.StartScrub(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Capture a running scrub and a serialized partial SHA256 state too.
	if _, err = s.ScrubStep(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "backup")
	info, err := Backup(t.Context(), s.root, backup)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyBackup(t.Context(), backup)
	if err != nil || verified != info || info.Files < 10 || info.Bytes < 5<<20 {
		t.Fatal(info, verified, err)
	}
	if err = os.RemoveAll(s.root); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), "restored")
	if got, err := RestoreBackup(t.Context(), backup, restored); err != nil || got != info {
		t.Fatal(got, err)
	}
	node, err := Open(restored)
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	if readText(t, node, "../../literal +key.txt") != "durable data" || readText(t, node, "empty") != "" || readText(t, node, "damaged") != "replacement" {
		t.Fatal("restored object bytes differ")
	}
	if _, err = node.bucket("empty-bucket"); err != nil {
		t.Fatal("empty bucket lost", err)
	}
	for id, status := range map[string]string{committed: ReceiptCommitted, rejected: ReceiptRejected} {
		proof, err := node.Receipt("test-bucket", "historical", id)
		if err != nil || proof.Status != status || id == committed && proof.Object.ID != original.ID {
			t.Fatal("historical write proof lost", proof, err)
		}
	}
	if _, _, err = node.Get("test-bucket", "historical"); !errors.Is(err, ErrNoObject) {
		t.Fatal("historical receipt resurrected an object", err)
	}
	proof, err := node.MultipartReceipt("test-bucket", completedUpload.Key, completedUpload.ReceiptID)
	if err != nil || proof.Status != ReceiptCommitted || proof.Object.ID != completed.ID {
		t.Fatal("multipart receipt lost", proof, err)
	}
	parts, _, err := node.ListParts(u.Bucket, u.Key, u.ID, 0, 10)
	if err != nil || len(parts) != 1 || parts[0].Object.ID != first.Object.ID {
		t.Fatal("multipart resume lost selected part", parts, err)
	}
	last := multipartPart(t, node, u, 2, []byte("tail"))
	if _, err = node.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, completion(first, last), nil); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, node, u.Key); got != strings.Repeat("a", 5<<20)+"tail" {
		t.Fatal("resumed assembly has different bytes")
	}
	after, err := node.ScrubFindings(t.Context(), "", 10)
	if err != nil || len(after.Items) != 1 || after.Items[0].Current || !after.Items[0].CurrentKnown || !after.Items[0].FirstDetectedAt.Equal(findings.Items[0].FirstDetectedAt) {
		t.Fatal("scrub history lost", after, err)
	}
	finishTestScrub(t, node, 64<<20)
	// Restoring and serving the copy must leave the backup independently usable.
	if _, err = VerifyBackup(t.Context(), backup); err != nil {
		t.Fatal("restore modified its backup", err)
	}
}

func TestBackupFinishesPendingIntent(t *testing.T) {
	s := testStore(t)
	putText(t, s, "key", "old")
	id := uuid.NewString()
	s.persist = func(path string, value any) (bool, error) {
		if strings.HasSuffix(path, "current.json") {
			return false, errors.New("interrupted before pointer")
		}
		return writeJSON(path, value)
	}
	if _, err := receiptPut(t, s, "key", "new", id, PutOptions{}); err == nil {
		t.Fatal("fault not reached")
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
	proof, err := node.Receipt("test-bucket", "key", id)
	if err != nil || proof.Status != ReceiptCommitted || readText(t, node, "key") != "new" {
		t.Fatal(proof, err)
	}
}

func TestBackupRefusesBadSource(t *testing.T) {
	for _, kind := range []string{"running", "object checksum", "part checksum", "missing blob", "symlink", "hard link", "missing source", "uninitialized source"} {
		t.Run(kind, func(t *testing.T) {
			s := testStore(t)
			putText(t, s, "key", "original")
			blob := scrubObjectPath(t, s, "key")
			if kind == "part checksum" {
				u := newMultipart(t, s, objectchecksum.Policy{})
				p := multipartPart(t, s, u, 1, []byte("original"))
				blob = filepath.Join(s.partPath(u.ID, 1), p.Object.ID+".data")
			}
			if kind != "running" {
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
			}
			source := s.root
			var err error
			switch kind {
			case "object checksum", "part checksum":
				err = os.WriteFile(blob, []byte("damaged!"), 0o600)
			case "missing blob":
				err = os.Remove(blob)
			case "symlink":
				if err = os.Remove(blob); err == nil {
					err = os.Symlink(filepath.Join(t.TempDir(), "outside"), blob)
				}
			case "hard link":
				err = os.Link(blob, filepath.Join(t.TempDir(), "alias"))
			case "missing source":
				source = filepath.Join(t.TempDir(), "missing")
			case "uninitialized source":
				source = t.TempDir()
			}
			if err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(t.TempDir(), "backup")
			if _, err = Backup(t.Context(), source, destination); err == nil {
				t.Fatal("unverified source accepted")
			}
			if _, err = os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed backup was published", err)
			}
		})
	}
}

func TestBackupRejectsDamagedArtifact(t *testing.T) {
	for _, kind := range []string{"data", "truncated data", "metadata", "manifest", "missing manifest", "extra file", "missing file", "symlink", "hard link", "duplicate path", "traversal", "duplicate field", "wrong digest"} {
		t.Run(kind, func(t *testing.T) {
			s, backup := backupFixture(t)
			o, err := readObject(s.objectPath("test-bucket", "key"))
			if err != nil {
				t.Fatal(err)
			}
			blob := filepath.Join(backup, "data", "buckets", nameHash("test-bucket"), "objects", nameHash("key"), o.ID+".data")
			switch kind {
			case "data":
				err = os.WriteFile(blob, []byte("damaged!"), 0o600)
			case "truncated data":
				err = os.Truncate(blob, 1)
			case "metadata":
				err = os.WriteFile(filepath.Join(backup, "backup.json"), []byte("{}"), 0o600)
			case "manifest":
				err = os.WriteFile(filepath.Join(backup, "manifest.jsonl"), []byte("garbage\n"), 0o600)
			case "missing manifest":
				err = os.Remove(filepath.Join(backup, "manifest.jsonl"))
			case "extra file":
				err = os.WriteFile(filepath.Join(backup, "data", "extra"), []byte("x"), 0o600)
			case "missing file":
				err = os.Remove(blob)
			case "symlink":
				if err = os.Remove(blob); err == nil {
					err = os.Symlink("/etc/passwd", blob)
				}
			case "hard link":
				err = os.Link(blob, filepath.Join(t.TempDir(), "alias"))
			default:
				rewriteTestManifest(t, backup, kind)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = VerifyBackup(t.Context(), backup); err == nil {
				t.Fatal("damaged backup verified")
			}
			destination := filepath.Join(t.TempDir(), "restored")
			if _, err = RestoreBackup(t.Context(), backup, destination); err == nil {
				t.Fatal("damaged backup restored")
			}
			if _, err = os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed restore published a node", err)
			}
		})
	}
}

func rewriteTestManifest(t *testing.T, backup, kind string) {
	t.Helper()
	info, err := readBackupInfo(backup)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(backup, "manifest.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSuffix(body, []byte("\n")), []byte("\n"))
	switch kind {
	case "duplicate path":
		lines[len(lines)-1] = lines[0]
	case "traversal":
		var e backupEntry
		if err = json.Unmarshal(lines[0], &e); err != nil {
			t.Fatal(err)
		}
		e.Path = "../outside"
		lines[0], err = json.Marshal(e)
	case "duplicate field":
		lines[0] = bytes.Replace(lines[0], []byte(`{"path":`), []byte(`{"path":"../outside","path":`), 1)
	case "wrong digest":
		for i, line := range lines {
			var e backupEntry
			if err = json.Unmarshal(line, &e); err != nil {
				t.Fatal(err)
			}
			if !e.Directory {
				e.SHA256 = strings.Repeat("0", 64)
				lines[i], err = json.Marshal(e)
				break
			}
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	body = append(bytes.Join(lines, []byte("\n")), '\n')
	sum := sha256.Sum256(body)
	info.ManifestBytes, info.ManifestSHA256 = int64(len(body)), hex.EncodeToString(sum[:])
	if err = os.WriteFile(filepath.Join(backup, "manifest.jsonl"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = writeJSON(filepath.Join(backup, "backup.json"), info); err != nil {
		t.Fatal(err)
	}
}

func TestBackupDestinationAndCancellation(t *testing.T) {
	s, backup := backupFixture(t)
	if _, err := Backup(t.Context(), s.root, backup); !errors.Is(err, os.ErrExist) {
		t.Fatal("existing backup accepted", err)
	}
	if _, err := RestoreBackup(t.Context(), backup, s.root); !errors.Is(err, os.ErrExist) {
		t.Fatal("existing node replaced", err)
	}
	if _, err := Backup(t.Context(), s.root, filepath.Join(s.root, "nested")); !errors.Is(err, ErrInvalid) {
		t.Fatal("nested backup accepted", err)
	}
	if _, err := RestoreBackup(t.Context(), backup, filepath.Join(backup, "nested")); !errors.Is(err, ErrInvalid) {
		t.Fatal("restore nested in backup accepted", err)
	}
	alias := filepath.Join(t.TempDir(), "source-alias")
	if err := os.Symlink(s.root, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := Backup(t.Context(), s.root, filepath.Join(alias, "nested")); !errors.Is(err, ErrInvalid) {
		t.Fatal("parent symlink bypassed disjoint paths", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	path := filepath.Join(t.TempDir(), "cancelled")
	if _, err := Backup(ctx, s.root, path); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := RestoreBackup(ctx, backup, path); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancellation published destination", err)
	}
	if _, err := VerifyBackup(t.Context(), backup); err != nil {
		t.Fatal("refused operations changed backup", err)
	}
}

func TestRestorePreservesMultipartExpiry(t *testing.T) {
	s := testStore(t)
	u := newMultipart(t, s, objectchecksum.Policy{})
	multipartPart(t, s, u, 1, []byte("part"))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	backup, restored := filepath.Join(t.TempDir(), "backup"), filepath.Join(t.TempDir(), "restored")
	if _, err := Backup(t.Context(), s.root, backup); err != nil {
		t.Fatal(err)
	}
	// Create an old but internally valid fixture. Neither checksum verification
	// nor restore is allowed to grant the session a fresh TTL.
	var session MultipartUpload
	sessionPath := filepath.Join(backup, "data", "multipart", u.ID, "session.json")
	if err := readJSON(sessionPath, &session); err != nil {
		t.Fatal(err)
	}
	session.CreatedAt, session.ExpiresAt = time.Now().Add(-48*time.Hour), time.Now().Add(-time.Hour)
	if _, err := writeJSON(sessionPath, sealMultipart(session)); err != nil {
		t.Fatal(err)
	}
	rehashTestBackup(t, backup)
	if _, err := RestoreBackup(t.Context(), backup, restored); err != nil {
		t.Fatal(err)
	}
	node, err := Open(restored)
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	got, err := node.MultipartInfo(u.Bucket, u.Key, u.ID)
	if err != nil || got.Status != multipartExpired || !got.ExpiresAt.Equal(session.ExpiresAt) {
		t.Fatal("restore extended multipart TTL", got, err)
	}
}

func rehashTestBackup(t *testing.T, backup string) {
	t.Helper()
	info, err := readBackupInfo(backup)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(backup, "manifest.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var rebuilt []byte
	info.Bytes = 0
	for _, line := range bytes.Split(bytes.TrimSuffix(body, []byte("\n")), []byte("\n")) {
		var e backupEntry
		if err = json.Unmarshal(line, &e); err != nil {
			t.Fatal(err)
		}
		if !e.Directory {
			data, err := os.ReadFile(filepath.Join(backup, "data", e.Path))
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(data)
			e.Size, e.SHA256 = int64(len(data)), hex.EncodeToString(sum[:])
			info.Bytes += e.Size
		}
		encoded, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		rebuilt = append(rebuilt, encoded...)
		rebuilt = append(rebuilt, '\n')
	}
	sum := sha256.Sum256(rebuilt)
	info.ManifestBytes, info.ManifestSHA256 = int64(len(rebuilt)), hex.EncodeToString(sum[:])
	if err = os.WriteFile(filepath.Join(backup, "manifest.jsonl"), rebuilt, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = writeJSON(filepath.Join(backup, "backup.json"), info); err != nil {
		t.Fatal(err)
	}
}

// Keep the metadata-bearing PUT path part of the recovery drill too.
func TestBackupRestoresObjectMetadata(t *testing.T) {
	s := testStore(t)
	options := PutOptions{Headers: http.Header{"Content-Type": {"application/custom"}, "X-Amz-Meta-Name": {"value"}}}
	o, err := s.Put(t.Context(), "test-bucket", "metadata", strings.NewReader("body"), 4, options)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	backup, restored := filepath.Join(t.TempDir(), "backup"), filepath.Join(t.TempDir(), "restored")
	if _, err = Backup(t.Context(), s.root, backup); err != nil {
		t.Fatal(err)
	}
	if _, err = RestoreBackup(t.Context(), backup, restored); err != nil {
		t.Fatal(err)
	}
	node, err := Open(restored)
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	got, file, err := node.Get("test-bucket", "metadata")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if got.ID != o.ID || got.ETag != o.ETag || got.SHA256 != o.SHA256 || !got.Modified.Equal(o.Modified) || got.Headers.Get("Content-Type") != "application/custom" || got.Headers.Get("X-Amz-Meta-Name") != "value" {
		t.Fatal("metadata changed", got)
	}
}
