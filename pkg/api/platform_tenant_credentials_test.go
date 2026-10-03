package api

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestPreparePlatformTenantCredential(t *testing.T) {
	intent, plaintext, err := PreparePlatformTenantCredential("consumer-id", "v1", []string{"read"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plaintext) != len("ck_")+8+1+64 || plaintext[:3] != "ck_" || plaintext[3:11] != intent.Prefix {
		t.Fatalf("invalid credential format or prefix")
	}
	want, err := hex.DecodeString(intent.Hash)
	if err != nil || !bytes.Equal(want, HashAPIKey(plaintext)) {
		t.Fatal("intent hash does not match plaintext")
	}
	if intent.ConsumerID != "consumer-id" || intent.Name != "v1" || len(intent.Scopes) != 1 {
		t.Fatalf("intent = %+v", intent)
	}
}
