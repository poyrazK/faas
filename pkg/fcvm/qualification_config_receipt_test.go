package fcvm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestQualificationConfigReceiptBindsGuestAndDeliveredInputs(t *testing.T) {
	m := NewManager(&fakeRunner{}, nil, Paths{}, "test", nil, nil)
	request := WakeRequest{Instance: uuid.NewString(), APIEnvEntries: []APIEnvEntry{{Key: "MODE", Value: "reviewed"}},
		SealedEnvEntries: []SealedEnvEntry{{Key: "DATABASE_URL", Ciphertext: []byte("ciphertext")}}}
	prepared, waiter, err := m.prepareQualificationConfigReceipt(request)
	if err != nil {
		t.Fatal(err)
	}
	defer m.clearQualificationConfigReceipt(request.Instance, waiter)
	if len(request.APIEnvEntries) != 1 || len(prepared.APIEnvEntries) != 3 {
		t.Fatalf("receipt injection mutated caller or omitted control key: original=%+v prepared=%+v", request.APIEnvEntries, prepared.APIEnvEntries)
	}
	tokenControl, macKeyControl := prepared.APIEnvEntries[1], prepared.APIEnvEntries[2]
	if tokenControl.Key != qualificationConfigReceiptTokenKey || tokenControl.Value != waiter.token || macKeyControl.Key != qualificationConfigReceiptMACKey {
		t.Fatalf("unexpected internal receipt controls: %+v %+v", tokenControl, macKeyControl)
	}
	macKey, err := hex.DecodeString(macKeyControl.Value)
	if err != nil || len(macKey) != sha256.Size {
		t.Fatalf("invalid internal receipt MAC key: %v", err)
	}
	wantMAC, err := qualificationSecretKeysMAC(macKey, []string{"DATABASE_URL"})
	if err != nil || waiter.secretKeysMAC != wantMAC {
		t.Fatalf("secret key proof = %q, %v; want %q", waiter.secretKeysMAC, err, wantMAC)
	}
	receipt := EnvironmentQualificationConfigReceipt{Token: waiter.token, APIEnvSHA256: waiter.apiEnvSHA256,
		SecretsFileRead: true, SecretKeysMAC: waiter.secretKeysMAC}
	if err := m.MarkEnvironmentQualificationConfigApplied(request.Instance, receipt); err != nil {
		t.Fatal(err)
	}
	if err := m.waitForQualificationConfigReceipt(context.Background(), request.Instance, waiter); err != nil {
		t.Fatal(err)
	}
	m.clearQualificationConfigReceipt(request.Instance, waiter)
	if err := m.MarkEnvironmentQualificationConfigApplied(request.Instance, receipt); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("duplicate event after waiter cleanup should be unowned: %v", err)
	}
}

func TestQualificationConfigReceiptRejectsWrongAttemptOrInputs(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(EnvironmentQualificationConfigReceipt) EnvironmentQualificationConfigReceipt
	}{
		{name: "attempt token", mutate: func(r EnvironmentQualificationConfigReceipt) EnvironmentQualificationConfigReceipt {
			r.Token = uuid.NewString()
			return r
		}},
		{name: "api env", mutate: func(r EnvironmentQualificationConfigReceipt) EnvironmentQualificationConfigReceipt {
			r.APIEnvSHA256 = "changed"
			return r
		}},
		{name: "secret selection status", mutate: func(r EnvironmentQualificationConfigReceipt) EnvironmentQualificationConfigReceipt {
			r.SecretsFileRead = true
			return r
		}},
		{name: "secret key MAC", mutate: func(r EnvironmentQualificationConfigReceipt) EnvironmentQualificationConfigReceipt {
			r.SecretKeysMAC = "changed"
			return r
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewManager(&fakeRunner{}, nil, Paths{}, "test", nil, nil)
			instance, token := uuid.NewString(), uuid.NewString()
			waiter, err := m.registerQualificationConfigReceipt(instance, token, "digest", false, "keys")
			if err != nil {
				t.Fatal(err)
			}
			defer m.clearQualificationConfigReceipt(instance, waiter)
			receipt := tc.mutate(EnvironmentQualificationConfigReceipt{Token: token, APIEnvSHA256: "digest", SecretKeysMAC: "keys"})
			if err := m.MarkEnvironmentQualificationConfigApplied(instance, receipt); err != nil {
				t.Fatalf("mismatched receipt should fail the attempt without poisoning transport health: %v", err)
			}
			if err := m.waitForQualificationConfigReceipt(context.Background(), instance, waiter); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("wrong guest config did not fail qualification: %v", err)
			}
		})
	}
}

func TestPrepareQualificationConfigReceiptRejectsReservedKeys(t *testing.T) {
	m := NewManager(&fakeRunner{}, nil, Paths{}, "test", nil, nil)
	for _, request := range []WakeRequest{
		{Instance: uuid.NewString(), APIEnvEntries: []APIEnvEntry{{Key: qualificationConfigReceiptKeyPrefix + "APP", Value: "x"}}},
		{Instance: uuid.NewString(), SealedEnvEntries: []SealedEnvEntry{{Key: qualificationConfigReceiptKeyPrefix + "APP", Ciphertext: []byte("sealed")}}},
	} {
		if _, _, err := m.prepareQualificationConfigReceipt(request); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("reserved key accepted: %v", err)
		}
	}
}
