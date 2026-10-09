package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/onebox-faas/faas/pkg/role"
)

func TestProfiledRejectsUnsupportedHostRolesBeforeOpeningListeners(t *testing.T) {
	for _, observed := range []string{"control-plane", "unknown-role"} {
		t.Run(observed, func(t *testing.T) {
			t.Setenv("FAAS_PROFILED_ROLE", observed)
			err := run(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
			var refused *role.ErrRefused
			if !errors.As(err, &refused) {
				t.Fatalf("run with role %q = %v, want role refusal", observed, err)
			}
		})
	}
}
