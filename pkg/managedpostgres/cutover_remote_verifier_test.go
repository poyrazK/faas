package managedpostgres

import (
	"context"
	"testing"
)

type leasedCutoverVerifier struct {
	t     *testing.T
	calls int
}

func (v *leasedCutoverVerifier) VerifyCredential(context.Context, Binding, SealedCredential, Database) error {
	v.t.Fatal("runtime credential reached legacy decryptor")
	return ErrUnavailable
}
func (v *leasedCutoverVerifier) VerifyCutoverCredential(_ context.Context, c Cutover, m CutoverCredential, target Database) error {
	v.calls++
	if c.State != CutoverVerifying || c.LeaseToken == "" || m.State != "sealed" || target.ID != c.Target.ID || !validSealedCredential(m.Sealed) {
		v.t.Fatal("unleased or wrong staged credential")
	}
	return nil
}
func TestCutoverVerificationDelegatesLiveLease(t *testing.T) {
	s, _, c, now, _ := preparedCutover(t)
	v := &leasedCutoverVerifier{t: t}
	s.verifier = v
	if _, err := s.Verify(t.Context(), c.AccountID, c.ID); err != nil {
		t.Fatal(err)
	}
	verified, err := s.Reconcile(t.Context(), c.AccountID, c.ID)
	if err != nil || !verified.VerificationFresh(*now) || v.calls != len(c.Credentials) {
		t.Fatalf("verification state=%s calls=%d err=%v", verified.State, v.calls, err)
	}
}
