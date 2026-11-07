package api

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// AccountProfileUpdate is the PATCH /v1/account body. An empty
// DisplayName clears the name.
type AccountProfileUpdate struct {
	DisplayName string `json:"display_name"`
}

// AccountEmailChangeRequest is the POST /v1/account/email body.
type AccountEmailChangeRequest struct {
	Email           string `json:"email"`
	CurrentPassword string `json:"current_password,omitempty"`
	CSRFToken       string `json:"csrf_token,omitempty"`
}

// EmailVerificationResendResponse is POST /v1/auth/verify-email/resend.
// Status is "sent" or "already_verified".
type EmailVerificationResendResponse struct {
	Status string `json:"status"`
	Email  string `json:"email,omitempty"`
}

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
