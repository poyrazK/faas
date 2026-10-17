package objectnode

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func receiptPut(t *testing.T, s *Store, key, body, receipt string, options PutOptions) (Object, error) {
	t.Helper()
	options.Receipt = receipt
	return s.Put(t.Context(), "test-bucket", key, strings.NewReader(body), int64(len(body)), options)
}

func TestReceiptSurvivesOverwriteDeleteAndRestart(t *testing.T) {
	s := testStore(t)
	id := uuid.NewString()
	original, err := receiptPut(t, s, "key", "original", id, PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	putText(t, s, "key", "replacement")
	replay, err := receiptPut(t, s, "key", "original", id, PutOptions{})
	if err != nil || replay.ID != original.ID || readText(t, s, "key") != "replacement" {
		t.Fatal("replay changed a later write", replay, err)
	}
	if err = s.Delete("test-bucket", "key"); err != nil {
		t.Fatal(err)
	}
	if _, err = receiptPut(t, s, "key", "original", id, PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.Get("test-bucket", "key"); !errors.Is(err, ErrNoObject) {
		t.Fatal("replay resurrected a deleted object", err)
	}
	if err = s.DeleteBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	proof, err := reopened.Receipt("test-bucket", "key", id)
	if err != nil || proof.Status != ReceiptCommitted || proof.Object.ID != original.ID || proof.Object.ETag != original.ETag || proof.Object.Size != 8 {
		t.Fatal("lost historical receipt", proof, err)
	}
	if _, err = reopened.Receipt("test-bucket", "other-key", id); !errors.Is(err, ErrNoReceipt) {
		t.Fatal("receipt escaped key scope", err)
	}
	if err = reopened.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	if _, err = receiptPut(t, reopened, "key", "original", id, PutOptions{}); !errors.Is(err, ErrReceiptReuse) {
		t.Fatal("replay crossed bucket incarnation", err)
	}
	if _, _, err = reopened.Get("test-bucket", "key"); !errors.Is(err, ErrNoObject) {
		t.Fatal("bucket incarnation replay wrote bytes", err)
	}
}

func TestReceiptBindsRequest(t *testing.T) {
	for _, tc := range []struct {
		name, key, body string
		options         PutOptions
	}{
		{"content", "key", "xyz", PutOptions{}},
		{"size", "key", "abcd", PutOptions{}},
		{"key", "other-key", "abc", PutOptions{}},
		{"metadata", "key", "abc", PutOptions{Headers: http.Header{"Content-Type": {"image/png"}}}},
		{"conditions", "key", "abc", PutOptions{IfNoneMatch: "*"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			id := uuid.NewString()
			if _, err := receiptPut(t, s, "key", "abc", id, PutOptions{}); err != nil {
				t.Fatal(err)
			}
			if _, err := receiptPut(t, s, tc.key, tc.body, id, tc.options); !errors.Is(err, ErrReceiptReuse) {
				t.Fatal("accepted conflicting retry", err)
			}
			if readText(t, s, "key") != "abc" {
				t.Fatal("conflict modified the original object")
			}
		})
	}
}

func TestReceiptRetainsConditionalRejection(t *testing.T) {
	s := testStore(t)
	putText(t, s, "key", "current")
	id := uuid.NewString()
	options := PutOptions{IfNoneMatch: "*"}
	if _, err := receiptPut(t, s, "key", "candidate", id, options); !errors.Is(err, ErrPrecondition) {
		t.Fatal(err)
	}
	if err := s.Delete("test-bucket", "key"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err = receiptPut(t, reopened, "key", "candidate", id, options); !errors.Is(err, ErrPrecondition) {
		t.Fatal("rejected retry became an accepted write", err)
	}
	proof, err := reopened.Receipt("test-bucket", "key", id)
	if err != nil || proof.Status != ReceiptRejected || proof.Object.Size != 9 {
		t.Fatal(proof, err)
	}
	if _, _, err = reopened.Get("test-bucket", "key"); !errors.Is(err, ErrNoObject) {
		t.Fatal("rejected write has bytes", err)
	}
	if _, err = reopened.Receipt("test-bucket", "key", uuid.NewString()); !errors.Is(err, ErrNoReceipt) {
		t.Fatal("absence supplied terminal proof", err)
	}
}

func TestReceiptScopeAndIdentityValidation(t *testing.T) {
	s := testStore(t)
	id := uuid.NewString()
	if _, err := receiptPut(t, s, "key", "abc", id, PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateBucket("other-bucket"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(t.Context(), "other-bucket", "key", strings.NewReader("abc"), 3, PutOptions{Receipt: id}); !errors.Is(err, ErrReceiptReuse) {
		t.Fatal("receipt crossed bucket scope", err)
	}
	for _, invalid := range []string{"../../outside", "bad-id", "67D38255-18BB-456A-B326-A9535BBC5C64"} {
		if _, err := receiptPut(t, s, "key", "abc", invalid, PutOptions{}); !errors.Is(err, ErrInvalid) {
			t.Fatal("malformed receipt admitted", invalid, err)
		}
		if _, err := s.Receipt("test-bucket", "key", invalid); !errors.Is(err, ErrInvalid) {
			t.Fatal("malformed receipt queried", invalid, err)
		}
	}
}

func TestReceiptFinishesPendingBeforeLaterMutation(t *testing.T) {
	s := testStore(t)
	putText(t, s, "key", "old")
	id := uuid.NewString()
	s.persist = func(path string, value any) (bool, error) {
		if strings.HasSuffix(path, "current.json") {
			return false, errors.New("temporary disk fault")
		}
		return writeJSON(path, value)
	}
	if _, err := receiptPut(t, s, "key", "new", id, PutOptions{}); err == nil {
		t.Fatal("fault did not retain pending intent")
	}
	if _, _, err := s.Get("test-bucket", "key"); !errors.Is(err, ErrCorrupt) {
		t.Fatal("read bypassed unresolved intent", err)
	}
	s.persist = writeJSON
	putText(t, s, "other-key", "later mutation")
	proof, err := s.Receipt("test-bucket", "key", id)
	if err != nil || proof.Status != ReceiptCommitted || readText(t, s, "key") != "new" {
		t.Fatal("later mutation did not finish pending intent", proof, err)
	}
}

func TestReceiptConcurrentReplaysPublishOnce(t *testing.T) {
	s := testStore(t)
	id := uuid.NewString()
	var group sync.WaitGroup
	results := make(chan Object, 12)
	for range 12 {
		group.Go(func() {
			o, err := receiptPut(t, s, "key", "body", id, PutOptions{IfNoneMatch: "*"})
			if err != nil {
				t.Error(err)
			}
			results <- o
		})
	}
	group.Wait()
	close(results)
	current, f, err := s.Get("test-bucket", "key")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	for result := range results {
		if result.ID != current.ID {
			t.Fatal("replay republished a different blob")
		}
	}
}

func TestReceiptCrashBoundaries(t *testing.T) {
	for _, boundary := range []string{"before intent", "published intent", "before pointer", "published pointer", "before terminal", "published terminal"} {
		t.Run(boundary, func(t *testing.T) {
			s := testStore(t)
			putText(t, s, "key", "old")
			id := uuid.NewString()
			fault := errors.New("injected persistence failure")
			s.persist = func(path string, value any) (bool, error) {
				stage := "pointer"
				if r, ok := value.(writeReceipt); ok {
					stage = "intent"
					if r.Status == ReceiptCommitted {
						stage = "terminal"
					}
				}
				if boundary == "before "+stage {
					return false, fault
				}
				published, err := writeJSON(path, value)
				if err == nil && boundary == "published "+stage {
					return published, fault
				}
				return published, err
			}
			if _, err := receiptPut(t, s, "key", "new", id, PutOptions{}); !errors.Is(err, fault) {
				t.Fatal("did not reach fault", err)
			}
			if boundary != "before intent" && boundary != "published intent" {
				if err := s.Delete("test-bucket", "key"); !errors.Is(err, fault) {
					t.Fatal("later mutation bypassed unresolved intent", err)
				}
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(s.root)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			proof, err := reopened.Receipt("test-bucket", "key", id)
			wantBody := "new"
			if boundary == "before intent" {
				wantBody = "old"
				if !errors.Is(err, ErrNoReceipt) {
					t.Fatal("unpublished intent became proof", proof, err)
				}
			} else if err != nil || proof.Status != ReceiptCommitted {
				t.Fatal("durable intent not completed", proof, err)
			}
			if readText(t, reopened, "key") != wantBody {
				t.Fatal("recovery chose wrong write")
			}
		})
	}
}

func TestReceiptPendingCorruptionFailsClosed(t *testing.T) {
	for _, corruption := range []string{"journal", "missing blob", "same size blob"} {
		t.Run(corruption, func(t *testing.T) {
			s := testStore(t)
			id := uuid.NewString()
			s.persist = func(path string, value any) (bool, error) {
				if strings.HasSuffix(path, "current.json") {
					return false, errors.New("interrupted commit")
				}
				return writeJSON(path, value)
			}
			if _, err := receiptPut(t, s, "key", "body", id, PutOptions{}); err == nil {
				t.Fatal("fault not triggered")
			}
			r := *s.pending
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(s.objectPath("test-bucket", "key"), r.Object.ID+".data")
			var err error
			switch corruption {
			case "journal":
				r.Status = ReceiptRejected // A changed terminal status must invalidate the seal.
				_, err = writeJSON(s.receiptPath(id), r)
			case "missing blob":
				err = os.Remove(path)
			case "same size blob":
				err = os.WriteFile(path, []byte("evil"), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if reopened, err := Open(s.root); !errors.Is(err, ErrCorrupt) {
				if reopened != nil {
					reopened.Close()
				}
				t.Fatal("corrupt pending receipt produced proof", err)
			}
		})
	}
}

func TestReceiptKilledCommitRecovery(t *testing.T) {
	const id = "67d38255-18bb-456a-b326-a9535bbc5c64"
	if directory := os.Getenv("GREGALE_OBJECT_RECEIPT_CRASH_TEST"); directory != "" {
		s, err := Open(directory)
		if err != nil {
			t.Fatal(err)
		}
		s.persist = func(path string, value any) (bool, error) {
			published, err := writeJSON(path, value)
			if r, ok := value.(writeReceipt); ok && r.Status == receiptCommitting && err == nil {
				_, _ = os.Stdout.WriteString("intent durable\n")
				select {}
			}
			return published, err
		}
		_, _ = s.Put(context.Background(), "test-bucket", "key", strings.NewReader("committed"), 9, PutOptions{Receipt: id})
		return
	}
	s := testStore(t)
	putText(t, s, "key", "old")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReceiptKilledCommitRecovery$")
	command.Env = append(os.Environ(), "GREGALE_OBJECT_RECEIPT_CRASH_TEST="+s.root)
	out, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill() }()
	if scanner := bufio.NewScanner(out); !scanner.Scan() || scanner.Text() != "intent durable" {
		t.Fatal("child did not persist its commit intent")
	}
	if err = command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	reopened, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	proof, err := reopened.Receipt("test-bucket", "key", id)
	if err != nil || proof.Status != ReceiptCommitted || readText(t, reopened, "key") != "committed" {
		t.Fatal("killed commit not recovered", proof, err)
	}
}

func TestReceiptUpgradesDiskFormat(t *testing.T) {
	s := testStore(t)
	putText(t, s, "key", "old format")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.root, "format.json")
	if _, err := writeJSON(path, map[string]any{"engine": "gregale-object-node", "version": 1}); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var format struct {
		Engine  string `json:"engine"`
		Version int    `json:"version"`
	}
	if err = readJSON(path, &format); err != nil || format.Version != 3 || readText(t, reopened, "key") != "old format" {
		t.Fatal("format upgrade damaged existing data", format, err)
	}
}
