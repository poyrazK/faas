package main

import (
	"io"
	"log/slog"
	"testing"

	"github.com/onebox-faas/faas/pkg/internalmail"
)

func TestDisabledMaildNeedsNoDatabaseCredentialsOrListener(t *testing.T) {
	for _, flag := range []string{"", "0", "true"} {
		t.Run(flag, func(t *testing.T) {
			t.Setenv(internalmail.EnableEnv, flag)
			t.Setenv("DATABASE_URL", "invalid")
			t.Setenv("FAAS_INTERNAL_MAIL_AGE_IDENTITY_PATH", "/does/not/exist")
			t.Setenv("FAAS_INTERNAL_MAIL_KEYRING_PATH", "/does/not/exist/keyring.json")
			t.Setenv(internalmail.EncryptionRotationEnableEnv, "1")
			t.Setenv(internalmail.SuppressionControlsEnableEnv, "1")
			t.Setenv(internalmail.RecoveryEnableEnv, "1")
			t.Setenv(internalmail.RecoveryIDEnv, "invalid")
			t.Setenv("FAAS_INTERNAL_MAIL_SOCKET", "relative-invalid")
			if err := run(t.Context(), slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
				t.Fatal(err)
			}
		})
	}
}
