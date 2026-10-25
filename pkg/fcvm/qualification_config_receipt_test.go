package fcvm

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestQualificationEmptySecretKeysMatchGuestEncoding(t *testing.T) {
	key := make([]byte, sha256.Size)
	guestMAC := hmac.New(sha256.New, key)
	if _, err := guestMAC.Write([]byte("[]")); err != nil {
		t.Fatal(err)
	}
	want := hex.EncodeToString(guestMAC.Sum(nil))
	for _, keys := range [][]string{nil, {}} {
		got, err := qualificationSecretKeysMAC(key, keys)
		if err != nil || got != want {
			t.Fatalf("empty secret key MAC = %q, %v; want guest MAC %q", got, err, want)
		}
	}
}

func TestQualificationConfigReceiptBindsGuestAndDeliveredInputs(t *testing.T) {
	m := NewManager(&fakeRunner{}, nil, Paths{}, "test", nil, nil)
	request := WakeRequest{Instance: uuid.NewString(), APIEnvEntries: []APIEnvEntry{{Key: "MODE", Value: "reviewed"}},
		SealedEnvEntries: []SealedEnvEntry{{Key: "DATABASE_URL", Ciphertext: []byte("ciphertext")}},
		Sidecars:         []WorkloadSpec{{Name: "worker", preparedEnvJSON: []byte(`{"PUBLIC":"safe","TOKEN":"secret"}`)}}}
	prepared, waiter, err := m.prepareQualificationConfigReceipt(request)
	if err != nil {
		t.Fatal(err)
	}
	defer m.clearQualificationConfigReceipt(request.Instance, waiter)
	if len(request.APIEnvEntries) != 1 || len(prepared.APIEnvEntries) != 3 {
		t.Fatalf("receipt injection mutated caller or omitted control key: original=%+v prepared=%+v", request.APIEnvEntries, prepared.APIEnvEntries)
	}
	digest, err := QualificationAPIEnvSHA256(request.APIEnvEntries)
	if err != nil || digest != waiter.expected[WorkloadNameMain].apiEnvSHA256 {
		t.Fatalf("exported API env digest = %q, %v; want %q", digest, err, waiter.expected[WorkloadNameMain].apiEnvSHA256)
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
	if err != nil || waiter.expected[WorkloadNameMain].secretKeysMAC != wantMAC {
		t.Fatalf("secret key proof = %q, %v; want %q", waiter.expected[WorkloadNameMain].secretKeysMAC, err, wantMAC)
	}
	wantSidecarMAC, err := qualificationSidecarConfigMAC(macKey, map[string]string{"MODE": "reviewed"}, map[string]string{"PUBLIC": "safe", "TOKEN": "secret"})
	if err != nil || waiter.expected["worker"].configMAC != wantSidecarMAC {
		t.Fatalf("sidecar config proof = %q, %v; want %q", waiter.expected["worker"].configMAC, err, wantSidecarMAC)
	}
	mainReceipt := EnvironmentQualificationConfigReceipt{Token: waiter.token, Workload: WorkloadNameMain,
		APIEnvSHA256: waiter.expected[WorkloadNameMain].apiEnvSHA256, SecretsFileRead: true, SecretKeysMAC: wantMAC}
	if err := m.MarkEnvironmentQualificationConfigApplied(request.Instance, mainReceipt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-waiter.result:
		t.Fatal("main receipt completed the cohort before the sidecar receipt")
	default:
	}
	sidecarReceipt := EnvironmentQualificationConfigReceipt{Token: waiter.token, Workload: "worker", ConfigMAC: waiter.expected["worker"].configMAC}
	if err := m.MarkEnvironmentQualificationConfigApplied(request.Instance, sidecarReceipt); err != nil {
		t.Fatal(err)
	}
	if err := m.waitForQualificationConfigReceipt(context.Background(), request.Instance, waiter); err != nil {
		t.Fatal(err)
	}
	m.clearQualificationConfigReceipt(request.Instance, waiter)
	if err := m.MarkEnvironmentQualificationConfigApplied(request.Instance, mainReceipt); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("duplicate event after waiter cleanup should be unowned: %v", err)
	}
}

// A restore stages nothing: the fresh control rides the resume hook, and the
// guest re-attests its boot configuration: this request's projection under the
// capture's platform instance identity.
func TestQualificationRestoreConfigReceiptStagesNoControls(t *testing.T) {
	m := NewManager(&fakeRunner{}, nil, Paths{}, "test", nil, nil)
	captureInstance := uuid.NewString()
	request := WakeRequest{Instance: uuid.NewString(), APIEnvEntries: []APIEnvEntry{{Key: "MODE", Value: "reviewed"}},
		SealedEnvEntries: []SealedEnvEntry{{Key: "DATABASE_URL", Ciphertext: []byte("ciphertext")}}}
	request.APIEnvEntries = append(request.APIEnvEntries, APIEnvEntry{Key: api.PlatformInstanceIDEnv, Value: request.Instance})
	prepared, waiter, err := m.prepareQualificationRestoreConfigReceipt(request, captureInstance)
	if err != nil {
		t.Fatal(err)
	}
	defer m.clearQualificationConfigReceipt(request.Instance, waiter)
	control := prepared.qualificationRestoreReceipt
	if !slices.Equal(prepared.APIEnvEntries, request.APIEnvEntries) || control.token != waiter.token || !control.valid() || prepared.qualificationRestoreReceiptWaiter != waiter {
		t.Fatalf("restore receipt staged env controls or lost its control: %+v %+v", prepared.APIEnvEntries, control)
	}
	macKey, err := hex.DecodeString(control.macKey)
	if err != nil {
		t.Fatal(err)
	}
	wantMAC, err := qualificationSecretKeysMAC(macKey, []string{"DATABASE_URL"})
	// The captured guest booted as the capture instance; the restore request
	// names its own target, and nothing else differs.
	captured := []APIEnvEntry{{Key: "MODE", Value: "reviewed"}, {Key: api.PlatformInstanceIDEnv, Value: captureInstance}}
	digest, digestErr := QualificationAPIEnvSHA256(captured)
	exported, exportedErr := QualificationRestoreAPIEnvSHA256(request.APIEnvEntries, captureInstance)
	targetDigest, _ := QualificationAPIEnvSHA256(request.APIEnvEntries)
	expected := waiter.expected[WorkloadNameMain]
	if err != nil || digestErr != nil || exportedErr != nil || len(waiter.expected) != 1 || expected.secretKeysMAC != wantMAC ||
		expected.apiEnvSHA256 != digest || exported != digest || targetDigest == digest || !expected.secretsRead {
		t.Fatalf("restore receipt expectation = %+v", waiter.expected)
	}
	sidecars := request
	sidecars.Instance = uuid.NewString()
	sidecars.Sidecars = []WorkloadSpec{{Name: "worker"}}
	if _, _, err := m.prepareQualificationRestoreConfigReceipt(sidecars, captureInstance); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("restore receipt accepted sidecars: %v", err)
	}
	for _, capture := range []string{"", uuid.Nil.String(), strings.ToUpper(captureInstance)} {
		bad := request
		bad.Instance = uuid.NewString()
		if _, _, err := m.prepareQualificationRestoreConfigReceipt(bad, capture); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("restore receipt accepted capture instance %q: %v", capture, err)
		}
	}
	// Without a platform identity the restored env is the request's own.
	plain := []APIEnvEntry{{Key: "MODE", Value: "reviewed"}}
	plainDigest, _ := QualificationAPIEnvSHA256(plain)
	if got, err := QualificationRestoreAPIEnvSHA256(plain, captureInstance); err != nil || got != plainDigest {
		t.Fatalf("restore digest without platform identity = %q, %v", got, err)
	}
	for _, bad := range []qualificationRestoreReceiptControl{{}, {token: waiter.token}, {token: uuid.Nil.String(), macKey: control.macKey}, {token: waiter.token, macKey: control.macKey[:32]}} {
		if bad.valid() {
			t.Fatalf("malformed restore receipt control accepted: %+v", bad)
		}
	}
	// The restorer consumes the receipt itself, without the Manager.
	var missing *qualificationConfigReceiptWaiter
	if err := missing.wait(context.Background(), request.Instance); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("restore without a registered receipt waiter = %v", err)
	}
	if err := m.MarkEnvironmentQualificationConfigApplied(request.Instance, EnvironmentQualificationConfigReceipt{Token: waiter.token, Workload: WorkloadNameMain,
		APIEnvSHA256: expected.apiEnvSHA256, SecretsFileRead: true, SecretKeysMAC: wantMAC}); err != nil {
		t.Fatal(err)
	}
	if err := prepared.qualificationRestoreReceiptWaiter.wait(context.Background(), request.Instance); err != nil {
		t.Fatal(err)
	}
}

func TestQualificationJobConfigReceiptExcludesHostControlsFromCommandConfig(t *testing.T) {
	m := NewManager(&fakeRunner{}, nil, Paths{}, "test", nil, nil)
	plain := map[string]string{"API_URL": "http://10.0.0.1:1027/private"}
	prepared, digest, waiter, err := m.prepareQualificationJobConfigReceipt("job-instance", plain, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.clearQualificationConfigReceipt("job-instance", waiter)
	if prepared["API_URL"] != plain["API_URL"] || len(prepared) != 3 || prepared[qualificationConfigReceiptTokenKey] != waiter.token {
		t.Fatalf("job env receipt controls = %#v", prepared)
	}
	wantDigest, err := qualificationAPIEnvSHA256(plain)
	if err != nil || digest != wantDigest || waiter.expected[WorkloadNameMain].apiEnvSHA256 != wantDigest {
		t.Fatalf("job config digest = %q, expected %#v, err=%v", digest, waiter.expected, err)
	}
	macKey, err := hex.DecodeString(prepared[qualificationConfigReceiptMACKey])
	if err != nil {
		t.Fatal(err)
	}
	defer clear(macKey)
	secretMAC, err := qualificationSecretKeysMAC(macKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkEnvironmentQualificationConfigApplied("job-instance", EnvironmentQualificationConfigReceipt{
		Token: waiter.token, Workload: WorkloadNameMain, APIEnvSHA256: wantDigest, SecretKeysMAC: secretMAC,
	}); err != nil {
		t.Fatalf("accept job guest config receipt: %v", err)
	}
	if err := m.waitForQualificationConfigReceipt(context.Background(), "job-instance", waiter); err != nil {
		t.Fatalf("wait for job guest config receipt: %v", err)
	}
}

func TestQualificationJobConfigReceiptBindsSealedEnvironmentKeys(t *testing.T) {
	m := NewManager(&fakeRunner{}, nil, Paths{}, "test", nil, nil)
	sealed := []SealedEnvEntry{{Key: "DATABASE_URL", SourceKey: "DATABASE", Ciphertext: []byte("sealed")}}
	prepared, _, waiter, err := m.prepareQualificationJobConfigReceipt("job-instance", map[string]string{"MODE": "safe"}, sealed)
	if err != nil {
		t.Fatal(err)
	}
	defer m.clearQualificationConfigReceipt("job-instance", waiter)
	macKey, err := hex.DecodeString(prepared[qualificationConfigReceiptMACKey])
	if err != nil {
		t.Fatal(err)
	}
	defer clear(macKey)
	wantMAC, err := qualificationSecretKeysMAC(macKey, []string{"DATABASE_URL"})
	if err != nil || !waiter.expected[WorkloadNameMain].secretsRead || waiter.expected[WorkloadNameMain].secretKeysMAC != wantMAC {
		t.Fatalf("qualification job did not bind the sealed keys: expected=%+v want=%q err=%v", waiter.expected[WorkloadNameMain], wantMAC, err)
	}
	wrongMAC, err := qualificationSecretKeysMAC(macKey, []string{"OTHER_SECRET"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkEnvironmentQualificationConfigApplied("job-instance", EnvironmentQualificationConfigReceipt{
		Token: waiter.token, Workload: WorkloadNameMain, APIEnvSHA256: waiter.expected[WorkloadNameMain].apiEnvSHA256,
		SecretsFileRead: true, SecretKeysMAC: wrongMAC,
	}); err != nil {
		t.Fatalf("mismatched guest receipt should be recorded as a failed attempt: %v", err)
	}
	if err := m.waitForQualificationConfigReceipt(context.Background(), "job-instance", waiter); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("mismatched secret-key receipt error = %v, want conflict", err)
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
			waiter, err := m.registerQualificationConfigReceipt(instance, token, map[string]qualificationConfigReceiptExpectation{
				WorkloadNameMain: {apiEnvSHA256: "digest", secretKeysMAC: "keys"},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer m.clearQualificationConfigReceipt(instance, waiter)
			receipt := tc.mutate(EnvironmentQualificationConfigReceipt{Token: token, Workload: WorkloadNameMain,
				APIEnvSHA256: "digest", SecretKeysMAC: "keys"})
			if err := m.MarkEnvironmentQualificationConfigApplied(instance, receipt); err != nil {
				t.Fatalf("mismatched receipt should fail the attempt without poisoning transport health: %v", err)
			}
			if err := m.waitForQualificationConfigReceipt(context.Background(), instance, waiter); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("wrong guest config did not fail qualification: %v", err)
			}
		})
	}
}

func TestQualificationConfigReceiptRejectsMismatchedSidecarMAC(t *testing.T) {
	m := NewManager(&fakeRunner{}, nil, Paths{}, "test", nil, nil)
	instance, token := uuid.NewString(), uuid.NewString()
	waiter, err := m.registerQualificationConfigReceipt(instance, token, map[string]qualificationConfigReceiptExpectation{
		"worker": {configMAC: "expected-mac"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.clearQualificationConfigReceipt(instance, waiter)
	receipt := EnvironmentQualificationConfigReceipt{Token: token, Workload: "worker", ConfigMAC: "different-mac"}
	if err := m.MarkEnvironmentQualificationConfigApplied(instance, receipt); err != nil {
		t.Fatalf("mismatched sidecar receipt should fail the attempt without poisoning transport health: %v", err)
	}
	if err := m.waitForQualificationConfigReceipt(context.Background(), instance, waiter); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("wrong sidecar config did not fail qualification: %v", err)
	}
}

func TestQualificationConfigReceiptRejectsInvalidSidecarProjection(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sidecars []WorkloadSpec
	}{
		{name: "invalid name", sidecars: []WorkloadSpec{{Name: "Worker"}}},
		{name: "duplicate name", sidecars: []WorkloadSpec{{Name: "worker"}, {Name: "worker"}}},
		{name: "malformed JSON", sidecars: []WorkloadSpec{{Name: "worker", preparedEnvJSON: []byte("not-json")}}},
		{name: "null JSON", sidecars: []WorkloadSpec{{Name: "worker", preparedEnvJSON: []byte("null")}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewManager(&fakeRunner{}, nil, Paths{}, "test", nil, nil)
			request := WakeRequest{Instance: uuid.NewString(), Sidecars: tc.sidecars}
			if _, waiter, err := m.prepareQualificationConfigReceipt(request); !errors.Is(err, state.ErrInvalidArgument) || waiter != nil {
				t.Fatalf("invalid sidecar projection accepted: waiter=%v err=%v", waiter, err)
			}
		})
	}
}

func TestPrepareSidecarEnvFilesPreservesQualificationProjection(t *testing.T) {
	m := NewManager(&fakeRunner{}, nil, Paths{}, "test", nil, nil)
	wantJSON := []byte(`{"TOKEN":"secret"}`)
	wantGrants := []string{"TOKEN"}
	request := WakeRequest{Sidecars: []WorkloadSpec{{Name: "worker", GrantedEnvNames: append([]string(nil), wantGrants...), preparedEnvJSON: append([]byte(nil), wantJSON...)}}}
	if err := m.prepareSidecarEnvFiles(&request); err != nil {
		t.Fatal(err)
	}
	if string(request.Sidecars[0].preparedEnvJSON) != string(wantJSON) || !slices.Equal(request.Sidecars[0].GrantedEnvNames, wantGrants) {
		t.Fatalf("second preparation changed the exact staged projection or grants: %+v", request.Sidecars[0])
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
