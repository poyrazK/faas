//go:build linux

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestTakeQualificationConfigReceiptControlRemovesValues(t *testing.T) {
	token := "123e4567-e89b-12d3-a456-426614174000"
	macKey := []byte("01234567890123456789012345678901")
	env := map[string]string{qualificationConfigReceiptTokenKey: token, qualificationConfigReceiptMACKey: hex.EncodeToString(macKey), "PUBLIC_CONFIG": "safe"}
	got, gotMACKey, err := takeQualificationConfigReceiptControl(env)
	if err != nil || got != token || string(gotMACKey) != string(macKey) || env["PUBLIC_CONFIG"] != "safe" {
		t.Fatalf("receipt control extraction = %q, %x, %v, env=%v", got, gotMACKey, err, env)
	}
	if _, exists := env[qualificationConfigReceiptTokenKey]; exists {
		t.Fatal("internal receipt token remained in the application environment")
	}
	if _, exists := env[qualificationConfigReceiptMACKey]; exists {
		t.Fatal("internal receipt MAC key remained in the application environment")
	}
}

func TestQualificationConfigReceiptFrameContainsOnlyConfigProof(t *testing.T) {
	token := "123e4567-e89b-12d3-a456-426614174000"
	macKey := []byte("01234567890123456789012345678901")
	frame, err := qualificationConfigReceiptFrame(token, macKey, map[string]string{"MODE": "reviewed"}, map[string]string{"DATABASE_URL": "secret-value"})
	if err != nil || len(frame) < 2 || frame[0] != vsockQualificationConfigReceiptType {
		t.Fatalf("receipt frame = %v, %v", frame, err)
	}
	var receipt qualificationConfigReceiptWire
	if err := json.Unmarshal(frame[1:], &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Token != token || receipt.APIEnvSHA256 == "" || !receipt.SecretsFileRead || receipt.SecretKeysMAC == "" {
		t.Fatalf("incomplete guest config receipt: %+v", receipt)
	}
	if strings.Contains(string(frame), "reviewed") || strings.Contains(string(frame), "secret-value") || strings.Contains(string(frame), "DATABASE_URL") {
		t.Fatal("receipt leaked configuration values or secret names")
	}
	otherToken := "123e4567-e89b-12d3-a456-426614174001"
	otherFrame, err := qualificationConfigReceiptFrame(otherToken, macKey, map[string]string{"MODE": "reviewed"}, map[string]string{"DATABASE_URL": "secret-value"})
	if err != nil {
		t.Fatal(err)
	}
	var otherReceipt qualificationConfigReceiptWire
	if err := json.Unmarshal(otherFrame[1:], &otherReceipt); err != nil {
		t.Fatal(err)
	}
	if otherReceipt.SecretKeysMAC != receipt.SecretKeysMAC {
		t.Fatal("same secret-key proof changed without changing its private MAC key")
	}
}

func TestQualificationConfigReceiptMACCannotBeRecomputedFromReceipt(t *testing.T) {
	token := "123e4567-e89b-12d3-a456-426614174000"
	macKey := []byte("01234567890123456789012345678901")
	frame, err := qualificationConfigReceiptFrame(token, macKey, nil, map[string]string{"DATABASE_URL": "secret-value"})
	if err != nil {
		t.Fatal(err)
	}
	var receipt qualificationConfigReceiptWire
	if err := json.Unmarshal(frame[1:], &receipt); err != nil {
		t.Fatal(err)
	}
	guessFrame, err := qualificationConfigReceiptFrame(token, []byte(token), nil, map[string]string{"DATABASE_URL": "secret-value"})
	if err != nil {
		t.Fatal(err)
	}
	var guess qualificationConfigReceiptWire
	if err := json.Unmarshal(guessFrame[1:], &guess); err != nil {
		t.Fatal(err)
	}
	if receipt.SecretKeysMAC == guess.SecretKeysMAC {
		t.Fatal("receipt MAC was derivable from its public attempt token")
	}
}

func TestTakeQualificationConfigReceiptControlRejectsIncompleteOrUnknownControl(t *testing.T) {
	for _, env := range []map[string]string{
		{qualificationConfigReceiptTokenKey: "123e4567-e89b-12d3-a456-426614174000"},
		{qualificationConfigReceiptTokenKey: "123e4567-e89b-12d3-a456-426614174000", qualificationConfigReceiptMACKey: hex.EncodeToString(make([]byte, sha256.Size)), qualificationConfigReceiptKeyPrefix + "EXTRA": "unexpected"},
	} {
		if _, _, err := takeQualificationConfigReceiptControl(env); err == nil {
			t.Fatalf("accepted malformed receipt control: %v", env)
		}
	}
}
