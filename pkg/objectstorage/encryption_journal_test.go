package objectstorage

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
)

// adr: 413
func TestS3EncryptionKeyRequestAccounting(t *testing.T) {
	var dataRequests atomic.Int32
	p, b := encryptionS3Fixture(t, func(w http.ResponseWriter, r *http.Request) { dataRequests.Add(1); w.WriteHeader(500) }, encryptionKeyResponse())
	e := encryptionSelection(t, b, "aws:kms")
	meterErr := errors.New("meter unavailable")
	var meters int
	ctx := WithEncryptionRequestRecorder(t.Context(), func(context.Context) error { meters++; return meterErr })
	if err := p.CheckEncryptionKey(ctx, e); !errors.Is(err, meterErr) || meters != 1 || dataRequests.Load() != 0 {
		t.Fatal("meter failure did not prevent native request", err, meters, dataRequests.Load())
	}
	bad := e
	bad.KeyIdentity = "changed"
	if err := p.CheckEncryptionKey(ctx, bad); !errors.Is(err, ErrConfiguration) || meters != 1 {
		t.Fatal("invalid binding billed as native request", err, meters)
	}
	if err := p.CheckEncryptionKey(ctx, encryptionSelection(t, b, "AES256")); err != nil || meters != 1 {
		t.Fatal("AES write billed a KMS request", err, meters)
	}
	ctx = WithEncryptionRequestRecorder(t.Context(), func(context.Context) error { meters++; return nil })
	if err := p.CheckEncryptionKey(ctx, e); err != nil || meters != 2 {
		t.Fatal("KMS request not accounted", err, meters)
	}
}
