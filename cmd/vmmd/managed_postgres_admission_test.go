// adr: 468
package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

type admissionReaderStore struct {
	state.Store
	fenced bool
	err    error
	appID  string
}

func (s *admissionReaderStore) ManagedPostgresAdmissionFenced(_ context.Context, appID string) (bool, error) {
	s.appID = appID
	return s.fenced, s.err
}

type noAdmissionReaderStore struct{ state.Store }

func TestManagedPostgresAdmissionGuard(t *testing.T) {
	readErr := errors.New("control-plane read failed")
	for _, tc := range []struct {
		name  string
		store state.Store
		want  error
	}{
		{"unfenced", &admissionReaderStore{}, nil},
		{"fenced", &admissionReaderStore{fenced: true}, fcvm.ErrAppAdmissionFenced},
		{"read_error", &admissionReaderStore{err: readErr}, fcvm.ErrAppAdmissionUnavailable},
		{"unsupported_store", &noAdmissionReaderStore{}, fcvm.ErrAppAdmissionUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			guard := managedPostgresAdmissionGuard(tc.store)
			err := guard(t.Context(), "app-fenced")
			if !errors.Is(err, tc.want) {
				t.Fatalf("guard error = %v, want %v", err, tc.want)
			}
			if reader, ok := tc.store.(*admissionReaderStore); ok && reader.appID != "app-fenced" {
				t.Fatal("guard did not check the resolved app identity")
			}
			if tc.name == "read_error" && !errors.Is(err, readErr) {
				t.Fatal("guard lost the underlying error")
			}
		})
	}
	if managedPostgresAdmissionGuard(nil) != nil {
		t.Fatal("DB-less legacy node unexpectedly claims admission support")
	}
}

func TestManagedPostgresAdmissionGuardPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	guard := managedPostgresAdmissionGuard(&admissionReaderStore{err: context.Canceled})
	if err := guard(ctx, "app-fenced"); !errors.Is(err, context.Canceled) || errors.Is(err, fcvm.ErrAppAdmissionUnavailable) {
		t.Fatalf("stop cancellation became an admission outage: %v", err)
	}
}

func TestRunDefaultLocalOpensConfiguredAdmissionDatabase(t *testing.T) {
	for _, source := range []string{"toml", "env"} {
		t.Run(source, func(t *testing.T) {
			dir := t.TempDir()
			config := "owner_user = \"root\"\n"
			t.Setenv("FAAS_VMMD_DBURL", "")
			const dsn = "postgres://admission-test"
			if source == "toml" {
				config += "db_url = \"" + dsn + "\"\n"
			} else {
				t.Setenv("FAAS_VMMD_DBURL", dsn)
			}
			configPath := filepath.Join(dir, "vmmd.toml")
			if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			load, recipient := nopHostKeyDeps(t)
			openErr := errors.New("admission DB unavailable")
			called := false
			deps := runDeps{
				configPath: configPath, capCheck: nopCapCheck(),
				detectFC:    func(context.Context) (string, error) { return "1.7.0", nil },
				loadHostKey: load, writeRecipient: recipient, loadHostKeys: nopHostKeysDep(t),
				openDB: func(_ context.Context, got string) (*pgxpool.Pool, error) {
					called = true
					if got != dsn {
						t.Fatalf("openDB DSN = %q", got)
					}
					return nil, openErr
				},
			}
			err := runWithDeps(t.Context(), slog.New(slog.NewTextHandler(io.Discard, nil)), deps)
			if !called || !errors.Is(err, openErr) {
				t.Fatalf("default-local node ignored or bypassed configured DB: %v", err)
			}
		})
	}
}
