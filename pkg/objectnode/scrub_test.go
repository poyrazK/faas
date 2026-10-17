package objectnode

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
	"github.com/onebox-faas/faas/pkg/objectinventory"
)

func finishTestScrub(t *testing.T, s *Store, budget int64) ScrubStatus {
	t.Helper()
	for range 100 {
		status, err := s.ScrubStep(t.Context(), budget)
		if err != nil {
			t.Fatal(err)
		}
		if status.State == "complete" {
			return status
		}
	}
	t.Fatal("scrub did not complete within its expected read budgets")
	return ScrubStatus{}
}

func scrubObjectPath(t *testing.T, s *Store, key string) string {
	t.Helper()
	_, f, err := s.Get("test-bucket", key)
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	_ = f.Close()
	return path
}

func TestScrubVerifiesObjectsPartsAndResumesAfterRestart(t *testing.T) {
	s := testStore(t)
	data := strings.Repeat("a", 4096)
	putText(t, s, "large", data)
	if _, err := s.StartScrub(t.Context()); err != nil {
		t.Fatal(err)
	}
	first, err := s.ScrubStep(t.Context(), 1024)
	if err != nil || first.CurrentOffset != 1024 || first.BytesRead != 1024 || first.Processed != 0 || first.State != "running" {
		t.Fatal(first, err)
	}
	if _, err = s.StartScrub(t.Context()); !errors.Is(err, ErrScrubBusy) {
		t.Fatal("active manifest was replaced", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	status, err := s.ScrubStatus()
	if err != nil || status.CurrentOffset != first.CurrentOffset || status.RunID != first.RunID {
		t.Fatal("restart lost hash progress", status, err)
	}
	status = finishTestScrub(t, s, 1024)
	if status.Verified != 1 || status.Failures != 0 || status.Stale != 0 || status.BytesRead != int64(len(data)) || status.LastVerifiedAt.IsZero() {
		t.Fatal(status)
	}
	putText(t, s, "empty", "")
	u := newMultipart(t, s, objectchecksum.Policy{})
	multipartPart(t, s, u, 1, []byte("part"))
	if _, err = s.StartScrub(t.Context()); err != nil {
		t.Fatal(err)
	}
	status = finishTestScrub(t, s, 1024)
	if status.Verified != 3 || status.Targets != 3 || status.BytesRead != int64(len(data))+4 || status.Failures != 0 {
		t.Fatal(status)
	}
	findings, err := s.ScrubFindings(t.Context(), "", 10)
	if err != nil || len(findings.Items) != 0 {
		t.Fatal(findings, err)
	}
	usage, err := s.Usage(t.Context())
	if err != nil || !usage.CatalogHealthy || usage.Warnings != 0 {
		t.Fatal("scrub metadata became orphan diagnostics", usage, err)
	}
}

func TestScrubDetectsSameSizeDamageAndRetainsRepairHistory(t *testing.T) {
	s := testStore(t)
	original := putText(t, s, "key", "original")
	path := scrubObjectPath(t, s, "key")
	if err := os.WriteFile(path, []byte("damaged!"), 0o600); err != nil {
		t.Fatal(err)
	}
	usage, err := s.Usage(t.Context())
	if err != nil || !usage.CatalogHealthy {
		t.Fatal("test must exercise damage invisible to file-length inventory", usage, err)
	}
	if _, err = s.StartScrub(t.Context()); err != nil {
		t.Fatal(err)
	}
	status := finishTestScrub(t, s, 4)
	if status.Failures != 1 || status.Verified != 0 || !status.LastVerifiedAt.IsZero() {
		t.Fatal(status)
	}
	findings, err := s.ScrubFindings(t.Context(), "", 10)
	if err != nil || len(findings.Items) != 1 {
		t.Fatal(findings, err)
	}
	finding := findings.Items[0]
	if finding.Reason != "checksum_mismatch" || finding.Target.BlobID != original.ID || finding.ObservedSHA256 == original.SHA256 || !finding.Current || !finding.CurrentKnown {
		t.Fatal(finding)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("scrubber deleted damaged evidence", err)
	}
	if err = os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartScrub(t.Context()); err != nil {
		t.Fatal(err)
	}
	status = finishTestScrub(t, s, 4)
	if status.Verified != 1 || status.Failures != 0 || status.LastVerifiedAt.IsZero() {
		t.Fatal(status)
	}
	findings, err = s.ScrubFindings(t.Context(), "", 10)
	if err != nil || len(findings.Items) != 1 || findings.Items[0].State != "verified" || !findings.Items[0].FirstDetectedAt.Equal(finding.FirstDetectedAt) || findings.Items[0].LastVerifiedAt.IsZero() {
		t.Fatal("verification erased corruption history", findings, err)
	}
	putText(t, s, "key", "replacement")
	findings, err = s.ScrubFindings(t.Context(), "", 10)
	if err != nil || findings.Items[0].Current || !findings.Items[0].CurrentKnown {
		t.Fatal("old finding was attributed to a replacement", findings, err)
	}
}

func TestScrubRewindsChangedFileAndSkipsReplacedBlob(t *testing.T) {
	for _, change := range []string{"same file", "overwrite", "delete", "bucket incarnation"} {
		t.Run(change, func(t *testing.T) {
			s := testStore(t)
			putText(t, s, "key", strings.Repeat("a", 1024))
			path := scrubObjectPath(t, s, "key")
			if _, err := s.StartScrub(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ScrubStep(t.Context(), 256); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "same file":
				f, err := os.OpenFile(path, os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = f.WriteAt([]byte("b"), 0); err != nil {
					t.Fatal(err)
				}
				_ = f.Close()
			case "overwrite":
				putText(t, s, "key", "replacement")
			case "delete":
				if err := s.Delete("test-bucket", "key"); err != nil {
					t.Fatal(err)
				}
			case "bucket incarnation":
				if err := s.Delete("test-bucket", "key"); err != nil {
					t.Fatal(err)
				}
				if err := s.DeleteBucket("test-bucket"); err != nil {
					t.Fatal(err)
				}
				if err := s.CreateBucket("test-bucket"); err != nil {
					t.Fatal(err)
				}
				putText(t, s, "key", "replacement")
			}
			status := finishTestScrub(t, s, 256)
			findings, err := s.ScrubFindings(t.Context(), "", 10)
			if err != nil {
				t.Fatal(err)
			}
			if change == "same file" {
				if status.Failures != 1 || status.BytesRead != 1280 || len(findings.Items) != 1 {
					t.Fatal("saved prefix hid changed bytes", status, findings)
				}
			} else if status.Stale != 1 || status.Failures != 0 || status.Verified != 0 || len(findings.Items) != 0 || !status.LastVerifiedAt.IsZero() {
				t.Fatal("replacement inherited an old result", status, findings)
			}
		})
	}
}

func TestScrubMultipartRacesAndCorruption(t *testing.T) {
	for _, change := range []string{"replace", "abort", "expire", "complete", "corrupt"} {
		t.Run(change, func(t *testing.T) {
			s := testStore(t)
			u := newMultipart(t, s, objectchecksum.Policy{})
			part := multipartPart(t, s, u, 1, bytes.Repeat([]byte("a"), 1024))
			if _, err := s.StartScrub(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ScrubStep(t.Context(), 256); err != nil {
				t.Fatal(err)
			}
			var err error
			switch change {
			case "replace":
				multipartPart(t, s, u, 1, []byte("new part"))
			case "abort":
				err = s.AbortMultipart(u.Bucket, u.Key, u.ID)
			case "expire":
				_, err = s.ExpireMultipart(t.Context(), time.Now().Add(api.ObjectMultipartUploadTTL+time.Second))
			case "complete":
				_, err = s.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, completion(part), nil)
			case "corrupt":
				err = os.WriteFile(filepath.Join(s.partPath(u.ID, 1), part.Object.ID+".data"), bytes.Repeat([]byte("b"), 1024), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			status := finishTestScrub(t, s, 256)
			findings, err := s.ScrubFindings(t.Context(), "", 10)
			if err != nil {
				t.Fatal(err)
			}
			if change == "corrupt" {
				if status.Failures != 1 || len(findings.Items) != 1 || findings.Items[0].Target.Kind != "part" || !findings.Items[0].Current {
					t.Fatal(status, findings)
				}
			} else if status.Stale != 1 || status.Failures != 0 || len(findings.Items) != 0 {
				t.Fatal(status, findings)
			}
		})
	}
}

func TestScrubMissingWrongSizeAndSymlink(t *testing.T) {
	for _, damage := range []string{"missing", "size", "symlink"} {
		t.Run(damage, func(t *testing.T) {
			s := testStore(t)
			putText(t, s, "key", "original")
			path := scrubObjectPath(t, s, "key")
			if damage == "size" {
				if err := os.Truncate(path, 1); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if damage == "symlink" {
					external := filepath.Join(t.TempDir(), "outside")
					if err := os.WriteFile(external, []byte("original"), 0o600); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(external, path); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := s.StartScrub(t.Context()); err != nil {
				t.Fatal(err)
			}
			status := finishTestScrub(t, s, 32)
			if status.Failures != 1 || status.BytesRead != 0 {
				t.Fatal(status)
			}
			findings, err := s.ScrubFindings(t.Context(), "", 10)
			if err != nil || len(findings.Items) != 1 {
				t.Fatal(findings, err)
			}
			want := map[string]string{"missing": "missing_blob", "size": "size_mismatch", "symlink": "invalid_file_type"}[damage]
			if findings.Items[0].Reason != want {
				t.Fatal(findings)
			}
		})
	}
}

func TestScrubCancellationAndInventoryIndependence(t *testing.T) {
	s := testStore(t)
	putText(t, s, "a", "abcd")
	putText(t, s, "z", "other")
	page := inventoryPage(t, s, objectinventory.Objects, "")
	if _, err := s.StartScrub(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ScrubStep(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	before, _ := s.ScrubStatus()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.ScrubStep(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	after, _ := s.ScrubStatus()
	if before.CurrentOffset != after.CurrentOffset || before.BytesRead != after.BytesRead {
		t.Fatal("cancellation published incomplete hash progress", before, after)
	}
	next := inventoryPage(t, s, objectinventory.Objects, page.NextCursor)
	if next.Snapshot != page.Snapshot || next.NextCursor != "" {
		t.Fatal("scrub checkpoints invalidated accounting inventory", page, next)
	}
}

func TestScrubReadFailureKeepsTargetPending(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses ordinary file permissions")
	}
	s := testStore(t)
	putText(t, s, "key", "original")
	path := scrubObjectPath(t, s, "key")
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	if _, err := s.StartScrub(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ScrubStep(t.Context(), 32); !errors.Is(err, os.ErrPermission) {
		t.Fatal("unreadable bytes did not remain uncertain", err)
	}
	status, err := s.ScrubStatus()
	if err != nil || status.Processed != 0 || status.Verified != 0 || status.Failures != 0 || status.State != "running" {
		t.Fatal("read failure changed an unverified target", status, err)
	}
	findings, err := s.ScrubFindings(t.Context(), "", 10)
	if err != nil || len(findings.Items) != 0 {
		t.Fatal("permission failure became a corruption claim", findings, err)
	}
	if err = os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	status = finishTestScrub(t, s, 32)
	if status.Verified != 1 || status.Failures != 0 {
		t.Fatal(status)
	}
}
