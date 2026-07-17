// Tests for the G2 secrets-staging path: cold-wake + restore both unseal
// the per-app blob, marshal it back to canonical JSON, and pass it to
// the VMM's StageSecretsEnv method. Failure modes covered:
//
//   - empty SealedEnvCiphertext ⇒ no StageSecretsEnv call (no-op)
//   - non-empty blob ⇒ StageSecretsEnv receives the unsealed JSON, NOT
//     the ciphertext
//   - missing host identity ⇒ wake fails fast with ErrNoHostKey; this is
//     the security-critical branch — silently dropping the blob would
//     leave the guest without secrets while the scheduler thinks the
//     wake succeeded
//   - tamper with ciphertext ⇒ Open fails and the wake fails; the
//     Manager refuses to boot a half-secure VM
//   - stageSecretsErr ⇒ the deferred cleanup path runs (no live instance
//     is registered)
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/secretbox"
)

// newIdentity returns a freshly generated X25519 identity. Each test
// gets its own — pairs created here are NOT used in production.
func newIdentity(t *testing.T) *age.X25519Identity {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func sealEnv(t *testing.T, id *age.X25519Identity, env secretbox.Envelope) []byte {
	t.Helper()
	blob, err := secretbox.Seal(id.Recipient(), env)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	return blob
}

// wakeRequestFor takes the standard `req(id)` ColdBootRequest and converts
// it into a WakeRequest with a sealed env blob. WakeRequest's private
// fields aren't settable from outside the package, but the test lives in
// the same package so the literal is straightforward.
func wakeRequestFor(id string, blob []byte) (ColdBootRequest, []byte) {
	return req(id), blob
}

func TestWake_EmptySealedEnv_NoStageCall(t *testing.T) {
	// Manager.Wake with no SealedEnvCiphertext should proceed through
	// bringUp without ever invoking StageSecretsEnv. The VMM stub records
	// any stage calls so we can assert none happened.
	vmm := &fakeVMM{}
	m := newTestManager(&fakeRunner{}, vmm)

	cb, blob := wakeRequestFor("no-secrets", nil)
	_ = blob
	inst, err := m.ColdBoot(context.Background(), cb)
	if err != nil {
		t.Fatalf("ColdBoot: %v", err)
	}
	if got := len(vmm.stagedSecrets); got != 0 {
		t.Errorf("StageSecretsEnv called %d times, want 0", got)
	}
	if inst == nil {
		t.Errorf("instance not returned")
	}
}

func TestWake_StageRoundTrip_UnsealsBeforeWrite(t *testing.T) {
	// StageSecretsEnv MUST receive the unsealed JSON, not the ciphertext.
	// This is what protects against a regression that ships the wrong
	// shape: the guest-init reader expects a JSON envelope, and feeding
	// it ciphertext would propagate a hard parse failure at every wake.
	id := newIdentity(t)
	env := secretbox.Envelope{"STRIPE_KEY": "sk_live_xyz", "DB_URL": "postgres://x"}
	blob := sealEnv(t, id, env)

	vmm := &fakeVMM{}
	m := newTestManager(&fakeRunner{}, vmm)
	m.SetHostIdentity(id)

	cb, _ := wakeRequestFor("stage-rt", blob)
	cb.SealedEnvCiphertext = blob

	if _, err := m.ColdBoot(context.Background(), cb); err != nil {
		t.Fatalf("ColdBoot: %v", err)
	}
	if got := len(vmm.stagedSecrets); got != 1 {
		t.Fatalf("StageSecretsEnv called %d times, want 1", got)
	}
	got := vmm.stagedSecrets[0].blob

	if strings.Contains(string(got), "AGE-") {
		t.Errorf("ciphertext leaked into the JSON blob fed to guest (AGE marker present): %q", got)
	}

	// Decode the JSON envelope to confirm shape.
	var open secretbox.Envelope
	if err := json.Unmarshal(got, &open); err != nil {
		t.Fatalf("stage blob not valid JSON envelope: %v (blob=%q)", err, got)
	}
	if open["STRIPE_KEY"] != "sk_live_xyz" || open["DB_URL"] != "postgres://x" {
		t.Errorf("secrets.env shape wrong: %+v", open)
	}
}

func TestWake_NoHostIdentity_RefusesSealedEnv(t *testing.T) {
	// Critical security invariant: a wake that arrives with a sealed
	// blob but no host age configured MUST fail — never silently drop
	// the blob and pretend the wake succeeded. The wake is reported
	// up the stack and the guest does NOT come up.
	id := newIdentity(t)
	blob := sealEnv(t, id, secretbox.Envelope{"K": "v"})
	vmm := &fakeVMM{}
	m := newTestManager(&fakeRunner{}, vmm)
	// No SetHostIdentity — hostIdentity stays nil.

	cb, _ := wakeRequestFor("no-key", blob)
	cb.SealedEnvCiphertext = blob

	_, err := m.ColdBoot(context.Background(), cb)
	if err == nil {
		t.Fatal("ColdBoot accepted sealed env without host key — that is a silent-data-loss bug")
	}
	if !errors.Is(err, ErrNoHostKey) {
		t.Errorf("err = %v, want ErrNoHostKey in chain", err)
	}
	if len(vmm.stagedSecrets) != 0 {
		t.Errorf("StageSecretsEnv was called despite missing key (%d calls)", len(vmm.stagedSecrets))
	}
}

func TestWake_TamperedCiphertext_FailsOpen(t *testing.T) {
	// A flipped byte in the ciphertext must reject the wake — the
	// Manager refuses to boot a VM whose env it cannot prove it staged.
	id := newIdentity(t)
	blob := sealEnv(t, id, secretbox.Envelope{"K": "v"})
	blob[len(blob)-1] ^= 0xFF
	vmm := &fakeVMM{}
	m := newTestManager(&fakeRunner{}, vmm)
	m.SetHostIdentity(id)

	cb, _ := wakeRequestFor("tamper", blob)
	cb.SealedEnvCiphertext = blob

	_, err := m.ColdBoot(context.Background(), cb)
	if err == nil {
		t.Fatal("ColdBoot should fail on tampered ciphertext")
	}
	if !strings.Contains(err.Error(), "open sealed env") {
		t.Errorf("error does not mention open sealed env: %v", err)
	}
	if len(vmm.stagedSecrets) != 0 {
		t.Errorf("StageSecretsEnv called despite failed open")
	}
}

func TestWake_StageErr_FailsWakeAndCleansUp(t *testing.T) {
	// When StageSecretsEnv itself returns an error (e.g. missing drive1
	// chroot), the deferred cleanup runs and the instance does NOT
	// appear in m.live.
	id := newIdentity(t)
	blob := sealEnv(t, id, secretbox.Envelope{"K": "v"})
	vmm := &fakeVMM{stageSecretsErr: errors.New("drive1 missing")}
	m := newTestManager(&fakeRunner{}, vmm)
	m.SetHostIdentity(id)

	cb, _ := wakeRequestFor("stage-fail", blob)
	cb.SealedEnvCiphertext = blob

	if _, err := m.ColdBoot(context.Background(), cb); err == nil {
		t.Fatal("ColdBoot should fail when StageSecretsEnv fails")
	}
	if _, ok := m.live["stage-fail"]; ok {
		t.Errorf("half-staged instance is registered — cleanup did not run")
	}
}
