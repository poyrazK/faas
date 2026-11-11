package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// uploadSHA256HexProblem refuses a sha256_hex that is not 64 hex digits, so
// the commit-time comparison below never sees an unusable value.
func uploadSHA256HexProblem(v string) *api.Problem {
	if v == "" {
		return nil
	}
	if len(v) == 64 && strings.Trim(strings.ToLower(v), "0123456789abcdef") == "" {
		return nil
	}
	return api.NewProblem(http.StatusBadRequest, api.CodeValidation,
		"Invalid sha256_hex", "sha256_hex must be the 64-digit hex SHA-256 of the archive")
}

// verifyUploadChecksum compares the received archive with the sha256_hex
// the client declared when it opened the session. The value used to be
// stored and never compared (deploy review 2026-11-10), so a chunk written
// at the wrong offset or corrupted in transit built anyway. On a mismatch the
// session is cancelled and its .part removed, and the 410 makes the CLI start
// a fresh upload instead of resuming the same bad bytes. A session opened
// without sha256_hex is not checked. The caller holds the session lock.
func (s *server) verifyUploadChecksum(ctx context.Context, row sqlc.UploadSession, ownerID string) (*api.Problem, error) {
	declared := strings.ToLower(row.Sha256Hex.String)
	if !row.Sha256Hex.Valid || declared == "" {
		return nil, nil
	}
	got, err := hashFileSHA256(row.PartPath)
	if err != nil {
		return nil, fmt.Errorf("hash received upload: %w", err)
	}
	if got == declared {
		return nil, nil
	}
	err = s.store.CancelUploadSession(ctx, sqlc.CancelUploadSessionParams{ID: row.ID, Column2: pgtypeFromUUIDString(ownerID)})
	if err != nil && !errors.Is(err, state.ErrConflict) && !errors.Is(err, state.ErrNotFound) {
		return nil, fmt.Errorf("cancel mismatched upload: %w", err)
	}
	if rmErr := removeUploadPart(row.PartPath); rmErr != nil && !os.IsNotExist(rmErr) {
		s.log.Warn("upload checksum mismatch: .part remove failed", "upload_id", row.ID, "err", rmErr)
	} else if clearErr := s.store.ClearUploadSessionPartPath(ctx, row.ID); clearErr != nil {
		s.log.Warn("upload checksum mismatch: clear part path failed", "upload_id", row.ID, "err", clearErr)
	}
	return api.ErrUploadChecksumMismatch(row.ID, declared, got), nil
}
