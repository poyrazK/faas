package auth

import (
	"errors"
	"testing"
)

func TestValidateRefusesCommonPasswords(t *testing.T) {
	for _, pw := range []string{
		"password1234", "Password1234", "QWERTY123456", "iloveyou1234",
		"aaaaaaaaaaaaa", "123456789012", "abcdefghijklm", "zyxwvutsrqpo", "890123456789",
	} {
		if err := Validate(pw); !errors.Is(err, ErrPasswordBreached) {
			t.Errorf("Validate(%q) = %v, want ErrPasswordBreached", pw, err)
		}
	}
	for _, pw := range []string{"correct-horse-battery-staple", "budget-password-123!", "Tq9!vmR2#lpX"} {
		if err := Validate(pw); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", pw, err)
		}
	}
	if err := Validate("short"); !errors.Is(err, ErrPasswordTooShort) {
		t.Errorf("Validate(short) = %v, want ErrPasswordTooShort", err)
	}
}

func TestBreachedListParsesEveryEntry(t *testing.T) {
	if len(breachedPasswords) < 100 {
		t.Fatalf("breached list has %d entries, want the bundled list", len(breachedPasswords))
	}
	for pw := range breachedPasswords {
		if len(pw) < MinPasswordLen {
			t.Errorf("list entry %q is shorter than MinPasswordLen and can never match", pw)
		}
	}
}
