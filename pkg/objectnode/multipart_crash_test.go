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

func TestMultipartKilledCommitRecovery(t *testing.T) {
	if directory := os.Getenv("GREGALE_MULTIPART_CRASH_TEST"); directory != "" {
		s, err := Open(directory)
		if err != nil {
			t.Fatal(err)
		}
		uploads, _, err := s.ListMultipart(context.Background(), "test-bucket", MultipartListRequest{Limit: 1})
		if err != nil || len(uploads) != 1 {
			t.Fatal(uploads, err)
		}
		u := uploads[0]
		parts, _, err := s.ListParts(u.Bucket, u.Key, u.ID, 0, 1)
		if err != nil || len(parts) != 1 {
			t.Fatal(parts, err)
		}
		s.persist = func(path string, value any) (bool, error) {
			published, err := writeJSON(path, value)
			if r, ok := value.(writeReceipt); ok && r.Kind == "multipart" && r.Status == receiptCommitting && err == nil {
				os.Stdout.WriteString("multipart intent durable\n")
				select {}
			}
			return published, err
		}
		_, _ = s.CompleteMultipart(context.Background(), u.Bucket, u.Key, u.ID, completion(parts[0]), nil)
		return
	}
	s := testStore(t)
	u := newMultipart(t, s, objectchecksum.Policy{})
	multipartPart(t, s, u, 1, []byte("survives process kill"))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMultipartKilledCommitRecovery$")
	command.Env = append(os.Environ(), "GREGALE_MULTIPART_CRASH_TEST="+s.root)
	out, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill() }()
	if scanner := bufio.NewScanner(out); !scanner.Scan() || scanner.Text() != "multipart intent durable" {
		t.Fatal("child did not reach durable intent")
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
	proof, err := reopened.MultipartReceipt(u.Bucket, u.Key, u.ReceiptID)
	if err != nil || proof.Status != ReceiptCommitted || readText(t, reopened, u.Key) != "survives process kill" {
		t.Fatal(proof, err)
	}
}

func TestMultipartCorruptPartsCannotComplete(t *testing.T) {
	for _, corruption := range []string{"bytes", "metadata"} {
		t.Run(corruption, func(t *testing.T) {
			s := testStore(t)
			u := newMultipart(t, s, objectchecksum.Policy{})
			p := multipartPart(t, s, u, 1, []byte("original"))
			dir := s.partPath(u.ID, 1)
			if corruption == "bytes" {
				if err := os.WriteFile(filepath.Join(dir, p.Object.ID+".data"), []byte("tampered"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				p.Object.ChecksumAlgorithm = "SHA256"
				p.Object.Checksum = strings.Repeat("A", 43) + "="
				if _, err := writeJSON(filepath.Join(dir, "current.json"), p); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, completion(p), nil); !errors.Is(err, ErrCorrupt) {
				t.Fatal("corrupt part completed", err)
			}
			if _, err := s.MultipartReceipt(u.Bucket, u.Key, u.ReceiptID); !errors.Is(err, ErrNoReceipt) {
				t.Fatal("corruption supplied terminal proof", err)
			}
			if _, _, err := s.Get(u.Bucket, u.Key); !errors.Is(err, ErrNoObject) {
				t.Fatal("corrupt bytes visible", err)
			}
		})
	}
}
