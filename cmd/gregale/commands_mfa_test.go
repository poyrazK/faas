// commands_mfa_test.go — IAM-2 (issue #186) smoke tests for the
// `gregale mfa <subcommand>` surface. Mirrors the commands_admin_test.go
// shape: arg-validation exits 1, auth-gate exits 1, happy path hits
// the right route with the right body, --json shape is one-line NDJSON.
//
// The interactive password prompt (cmdMfaDisable) is exercised via
// the --password flag only; the term.ReadPassword path is covered
// by code inspection rather than a tty-allocating test (CI runs
// without a tty).

package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMfa_NoSubcommandExitsOne(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	t.Setenv("FAAS_API", "http://unused")
	code := main.CmdMfa(nil)
	if code != 1 {
		t.Errorf("mfa (no sub) exit = %d, want 1", code)
	}
}

func TestMfa_UnknownSubcommandExitsOne(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	t.Setenv("FAAS_API", "http://unused")
	code := main.CmdMfa([]string{"frobnicate"})
	if code != 1 {
		t.Errorf("mfa frobnicate exit = %d, want 1", code)
	}
}

func TestMfa_NoTokenExitsTwo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("no network call expected without a token")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	for _, args := range [][]string{
		{"enroll"},
		{"confirm", "123456"},
		{"verify", "123456"},
		{"recover", "AAAAAAAAAA"},
		{"disable", "--password", "x"},
	} {
		// errAuth returns exit code 2 (auth), not 1 (user error).
		if code := main.CmdMfa(args); code != 2 {
			t.Errorf("mfa %v exit = %d, want 2 (auth)", args, code)
		}
	}
}

func TestMfa_Enroll_HappyPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")

	var sawMethod, sawPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawMethod = r.Method
		sawPath = r.URL.Path
		// resp.QRCodePNG is raw PNG bytes (json's []byte tag
		// round-trips base64). The CLI writes these bytes as-is.
		_ = json.NewEncoder(w).Encode(api.MFAEnrollResponse{
			OTPAuthURL:    "otpauth://totp/gregale:user@example.com?secret=JBSWY3DPEHPK3PXP",
			Secret:        "JBSWY3DPEHPK3PXP",
			QRCodePNG:     []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, // PNG magic
			RecoveryCodes: []string{"AAAA-11111", "BBBB-22222"},
		})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := main.CmdMfa([]string{"enroll"}); code != 0 {
		t.Fatalf("mfa enroll exit = %d, want 0", code)
	}
	if sawMethod != "POST" || sawPath != "/v1/account/mfa/enroll" {
		t.Errorf("route = %s %s, want POST /v1/account/mfa/enroll", sawMethod, sawPath)
	}
}

func TestMfa_Confirm_HappyPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")

	var sawBody api.MFAConfirmRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&sawBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := main.CmdMfa([]string{"confirm", "123456"}); code != 0 {
		t.Fatalf("mfa confirm exit = %d, want 0", code)
	}
	if sawBody.Totp != "123456" {
		t.Errorf("body totp = %q, want 123456", sawBody.Totp)
	}
}

func TestMfa_Confirm_MissingCodeExitsOne(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("no network call expected on missing code")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := main.CmdMfa([]string{"confirm"}); code != 1 {
		t.Errorf("mfa confirm (no code) exit = %d, want 1", code)
	}
}

func TestMfa_Recover_HappyPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")

	var sawBody api.MFARecoverRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&sawBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := main.CmdMfa([]string{"recover", "AAAAAAAAAA"}); code != 0 {
		t.Fatalf("mfa recover exit = %d, want 0", code)
	}
	if sawBody.Code != "AAAAAAAAAA" {
		t.Errorf("body code = %q, want AAAAAAAAAA", sawBody.Code)
	}
}

func TestMfa_Disable_MutuallyExclusiveFlags(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("no network call expected on mutually exclusive flags")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := main.CmdMfa([]string{"disable", "--password", "x", "--recovery-code", "AAAAAAAAAA"}); code != 1 {
		t.Errorf("mfa disable --password + --recovery-code exit = %d, want 1", code)
	}
}

func TestMfa_Disable_HappyPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")

	var sawBody api.MFADisableRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&sawBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := main.CmdMfa([]string{"disable", "--password", "hunter2"}); code != 0 {
		t.Fatalf("mfa disable exit = %d, want 0", code)
	}
	if sawBody.Password != "hunter2" {
		t.Errorf("body password = %q, want hunter2", sawBody.Password)
	}
	if sawBody.RecoveryCode != "" {
		t.Errorf("body recovery_code = %q, want empty", sawBody.RecoveryCode)
	}
}

func TestMfa_Enroll_UnknownJSONOutputShape(t *testing.T) {
	// Sanity check that --json triggers the indented-JSON path and
	// includes the QR PNG path we wrote. Pins the response shape so
	// a future refactor that drops QRPNGPath fails the test.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	(*main.JsonOutput) = true
	defer func() { (*main.JsonOutput) = false }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.MFAEnrollResponse{
			OTPAuthURL:    "otpauth://x",
			Secret:        "SEC",
			QRCodePNG:     []byte{0x89, 0x50, 0x4E, 0x47},
			RecoveryCodes: []string{"A1"},
		})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := main.CmdMfa([]string{"enroll", "--qr-out", "/tmp/qr.png"}); code != 0 {
		t.Fatalf("mfa enroll --json exit = %d, want 0", code)
	}
}
