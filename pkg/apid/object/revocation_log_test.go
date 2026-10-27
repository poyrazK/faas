package object

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

type revocationHost struct {
	Host
	log    *slog.Logger
	audits []string
}

func (h *revocationHost) Logger() *slog.Logger { return h.log }

func (h *revocationHost) EmitAudit(_ context.Context, kind string, _ *string, _ map[string]any) {
	h.audits = append(h.audits, kind)
}

// A restore rolls the audit log back with the revocation it records, so a
// revocation also goes to apid's log, which outlives the restore, with the
// IDs a replay needs.
func TestRevocationsAreLoggedOutsideTheDatabase(t *testing.T) {
	var out bytes.Buffer
	host := &revocationHost{log: slog.New(slog.NewJSONHandler(&out, nil))}
	s := &Service{Host: host}
	s.emitRevocation(context.Background(), "object_storage.s3_credential_revoked", "acct-1",
		map[string]any{"bucket_id": "bucket-1", "credential_id": "cred-1"})
	if len(host.audits) != 1 || host.audits[0] != "object_storage.s3_credential_revoked" {
		t.Fatalf("audits = %v", host.audits)
	}
	var line map[string]any
	if err := json.Unmarshal(out.Bytes(), &line); err != nil {
		t.Fatalf("log = %q: %v", out.String(), err)
	}
	for key, want := range map[string]string{"msg": "object storage access revoked", "kind": "object_storage.s3_credential_revoked",
		"account_id": "acct-1", "bucket_id": "bucket-1", "credential_id": "cred-1"} {
		if line[key] != want {
			t.Errorf("log %s = %v, want %q", key, line[key], want)
		}
	}
}
