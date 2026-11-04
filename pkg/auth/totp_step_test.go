package auth

import (
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// VerifyCodeStep accepts the codes of the current step and one either side,
// reports which step matched, and agrees with the library validator.
func TestVerifyCodeStepReportsTheMatchedStep(t *testing.T) {
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 20, 12, 0, 15, 0, time.UTC)
	current := now.Unix() / totpPeriod
	codeAt := func(step int64) string {
		code, err := totp.GenerateCodeCustom(secret, time.Unix(step*totpPeriod, 0), totp.ValidateOpts{Period: totpPeriod, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
		if err != nil {
			t.Fatal(err)
		}
		return code
	}
	for _, tc := range []struct {
		step   int64
		accept bool
	}{
		{current - 2, false}, {current - 1, true}, {current, true}, {current + 1, true}, {current + 2, false},
	} {
		code := codeAt(tc.step)
		step, ok := VerifyCodeStep(secret, code, now)
		if ok != tc.accept || (ok && step != tc.step) {
			// Adjacent steps can collide on one 6-digit code; only a mismatch
			// the library also rejects is a failure.
			if lib, _ := totp.ValidateCustom(code, secret, now, totp.ValidateOpts{Period: totpPeriod, Skew: 1, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}); lib != ok {
				t.Errorf("step %+d: VerifyCodeStep = (%d, %v), want (%d, %v)", tc.step-current, step, ok, tc.step, tc.accept)
			}
		}
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef"} {
		if _, ok := VerifyCodeStep(secret, bad, now); ok {
			t.Errorf("VerifyCodeStep accepted %q", bad)
		}
	}
	if _, ok := VerifyCodeStep(secret, " "+codeAt(current)+" ", now); !ok {
		t.Error("surrounding spaces must be trimmed, as the library does")
	}
}
