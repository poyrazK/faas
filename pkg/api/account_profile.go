package api

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SignInMethodsResponse is GET /v1/account/sign-in-methods.
type SignInMethodsResponse struct {
	Password  bool           `json:"password"`
	MFA       bool           `json:"mfa"`
	Providers []LinkedSignIn `json:"providers"`
}

// LinkedSignIn is one external identity that can sign in to the account.
type LinkedSignIn struct {
	Provider string `json:"provider"`
	Email    string `json:"email"`
	LinkedAt string `json:"linked_at"`
}

// RecoveryCodesResponse carries newly created MFA recovery codes, shown once.
type RecoveryCodesResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

// ValidateDisplayName trims raw and checks it against the accounts
// display_name constraint: at most DisplayNameMaxChars characters and no
// control characters. Empty clears the name.
func ValidateDisplayName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if !utf8.ValidString(name) {
		return "", errors.New("display_name must be valid UTF-8")
	}
	if n := utf8.RuneCountInString(name); n > DisplayNameMaxChars {
		return "", fmt.Errorf("display_name is %d characters; the limit is %d", n, DisplayNameMaxChars)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", errors.New("display_name must not contain control characters")
		}
	}
	return name, nil
}
