package objectnode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
)

func TestBackupRequiresTrackedObjectReceipt(t *testing.T) {
	for _, kind := range []string{"put", "multipart", "multipart session"} {
		t.Run(kind, func(t *testing.T) {
			s := testStore(t)
			id := uuid.NewString()
			var uploadID string
			if kind == "put" {
				if _, err := receiptPut(t, s, "key", "body", id, PutOptions{}); err != nil {
					t.Fatal(err)
				}
			} else {
				u := newMultipart(t, s, objectchecksum.Policy{})
				id, uploadID = u.ReceiptID, u.ID
				p := multipartPart(t, s, u, 1, []byte("body"))
				if _, err := s.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, completion(p), nil); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if kind == "multipart session" {
				if err := os.RemoveAll(s.multipartPath(uploadID)); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Remove(s.receiptPath(id)); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(t.TempDir(), "backup")
			if _, err := Backup(t.Context(), s.root, destination); err == nil {
				t.Fatal("live bytes hid missing recovery evidence")
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatal("unverified proof state was published", err)
			}
		})
	}
}
