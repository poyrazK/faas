package pgarchive

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/pgexport"
	"github.com/onebox-faas/faas/pkg/pgextension"
)

func TestArchiveReceiptAndPayload(t *testing.T) {
	body := []byte("PGDMP test backup contents")
	digest := sha256.Sum256(body)
	m := Manifest{
		Source:    pgexport.Source{Major: 16, CapturedAt: time.Now(), DatabaseBytes: 1024, Extensions: []pgextension.Installed{}},
		SizeBytes: int64(len(body)), SHA256: hex.EncodeToString(digest[:]),
	}
	receipt := m.Receipt("test-only exporter key")
	if !m.VerifyReceipt(receipt, "test-only exporter key") || m.VerifyReceipt(receipt, "another key") {
		t.Fatal("receipt did not authenticate the issuing key")
	}
	reencoded := m
	reencoded.CapturedAt = m.CapturedAt.In(time.FixedZone("SDK", 3*60*60))
	if !reencoded.VerifyReceipt(receipt, "test-only exporter key") {
		t.Fatal("changing the time zone changed the authenticated timestamp")
	}
	modified := m
	modified.DatabaseBytes++
	if modified.VerifyReceipt(receipt, "test-only exporter key") {
		t.Fatal("tampered metadata retained its receipt")
	}
	var output bytes.Buffer
	if err := m.Copy(&output, bytes.NewReader(body)); err != nil || !bytes.Equal(output.Bytes(), body) {
		t.Fatalf("verified payload failed: %v", err)
	}
	corrupt := bytes.Clone(body)
	corrupt[6] ^= 1
	for _, data := range [][]byte{corrupt, body[:len(body)-1], append(bytes.Clone(body), 1)} {
		if err := m.Copy(io.Discard, bytes.NewReader(data)); !errors.Is(err, ErrArchive) {
			t.Fatalf("accepted corrupted, shortened, or oversized archive: %v", err)
		}
	}
}
