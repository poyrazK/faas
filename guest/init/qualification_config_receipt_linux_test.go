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
	frame, err := qualificationConfigReceiptFrame("main", token, macKey, map[string]string{"MODE": "reviewed"}, map[string]string{"DATABASE_URL": "secret-value"}, nil)
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
	otherFrame, err := qualificationConfigReceiptFrame("main", otherToken, macKey, map[string]string{"MODE": "reviewed"}, map[string]string{"DATABASE_URL": "secret-value"}, nil)
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
	frame, err := qualificationConfigReceiptFrame("main", token, macKey, nil, map[string]string{"DATABASE_URL": "secret-value"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var receipt qualificationConfigReceiptWire
	if err := json.Unmarshal(frame[1:], &receipt); err != nil {
		t.Fatal(err)
	}
	guessKey := make([]byte, sha256.Size)
	guessFrame, err := qualificationConfigReceiptFrame("main", token, guessKey, nil, map[string]string{"DATABASE_URL": "secret-value"}, nil)
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

func TestSidecarConfigReceiptMACCoversEntireProjectionWithoutLeakingIt(t *testing.T) {
	token := "123e4567-e89b-12d3-a456-426614174000"
	macKey := []byte("01234567890123456789012345678901")
	env := map[string]string{"PUBLIC": "shown only in guest", "DATABASE_URL": "secret-value"}
	apiEnv := map[string]string{"LOG_LEVEL": "info"}
	frame, err := qualificationConfigReceiptFrame("worker", token, macKey, apiEnv, nil, env)
	if err != nil {
		t.Fatal(err)
	}
	var receipt qualificationConfigReceiptWire
	if err := json.Unmarshal(frame[1:], &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Workload != "worker" || receipt.ConfigMAC == "" || receipt.APIEnvSHA256 != "" || receipt.SecretKeysMAC != "" {
		t.Fatalf("unexpected sidecar config receipt: %+v", receipt)
	}
	if strings.Contains(string(frame), "shown only in guest") || strings.Contains(string(frame), "secret-value") || strings.Contains(string(frame), "DATABASE_URL") {
		t.Fatal("sidecar receipt leaked projection keys or values")
	}
	changed, err := qualificationConfigReceiptFrame("worker", token, macKey, apiEnv, nil, map[string]string{"PUBLIC": "shown only in guest", "DATABASE_URL": "other-secret"})
	if err != nil {
		t.Fatal(err)
	}
	var changedReceipt qualificationConfigReceiptWire
	if err := json.Unmarshal(changed[1:], &changedReceipt); err != nil {
		t.Fatal(err)
	}
	if changedReceipt.ConfigMAC == receipt.ConfigMAC {
		t.Fatal("sidecar receipt did not bind environment values")
	}
	changedAPI, err := qualificationConfigReceiptFrame("worker", token, macKey, map[string]string{"LOG_LEVEL": "debug"}, nil, env)
	if err != nil {
		t.Fatal(err)
	}
	var changedAPIReceipt qualificationConfigReceiptWire
	if err := json.Unmarshal(changedAPI[1:], &changedAPIReceipt); err != nil {
		t.Fatal(err)
	}
	if changedAPIReceipt.ConfigMAC == receipt.ConfigMAC {
		t.Fatal("sidecar receipt did not bind shared legacy API env")
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
