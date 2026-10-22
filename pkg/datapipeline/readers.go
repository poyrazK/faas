package datapipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline/sqlc"
)

// ReaderChange is separate from the schema workflow: access may be revoked
// while a table is quarantined or copying. ReadSubjects remains the applied set.
type ReaderChange struct {
	ID, State                   string
	Generation, PreviewRevision int64
	Before, After, Tables       []string
}

func validReaders(subjects []string) bool {
	if len(subjects) == 0 || len(subjects) > api.DataPipelineSubjectsMax {
		return false
	}
	seen := map[string]bool{}
	for _, subject := range subjects {
		if subject == "" || len(subject) > api.DataPipelineSubjectMaxBytes || strings.ContainsAny(subject, "\x00\r\n") || seen[subject] {
			return false
		}
		seen[subject] = true
	}
	return true
}

func CanChangeReaders(p Pipeline) bool {
	return p.Initialized && p.State != "resync_required" && p.DesiredState != "deleted" && p.State != "deleted" && (p.ReaderChange == nil || p.ReaderChange.State == "applied")
}

func readerChangeID(p Pipeline, c ReaderChange) string {
	c.ID, c.State = "", ""
	b, _ := json.Marshal(struct {
		Account, Pipeline string
		Change            ReaderChange
	}{p.AccountID, p.ID, c})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func canApproveReaders(p Pipeline) bool {
	c := p.ReaderChange
	return c != nil && c.State == "preview" && c.PreviewRevision == p.Revision && c.Generation == p.Generation && p.State != "resync_required" && p.DesiredState != "deleted" && slices.Equal(c.Before, p.ReadSubjects) && slices.Equal(c.Tables, p.Tables) && c.ID == readerChangeID(p, *c)
}

// Readers previews, approves, or discards an exact reader set. Approval never
// accepts a replacement list from the form; it uses only the stored preview.
func (s *Service) Readers(ctx context.Context, account, id string, revision int64, action, approval string, subjects []string) error {
	if err := s.admit(ctx, account); err != nil {
		return err
	}
	if !validID(account) || !validID(id) || revision < 1 {
		return ErrInvalid
	}
	if action != "preview" && action != "approve" && action != "discard" {
		return ErrInvalid
	}
	if action == "preview" && !validReaders(subjects) {
		return ErrInvalid
	}
	store, ok := s.Store.(interface {
		Readers(context.Context, string, string, int64, string, string, []string) error
	})
	if !ok {
		return ErrUnavailable
	}
	return store.Readers(ctx, account, id, revision, action, approval, subjects)
}

func (s *PostgresStore) Readers(ctx context.Context, account, id string, revision int64, action, approval string, subjects []string) error {
	p, err := s.Get(ctx, account, id)
	if errors.Is(err, ErrNotFound) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if p.Revision != revision {
		return ErrConflict
	}
	c := p.ReaderChange
	switch action {
	case "preview":
		if !validReaders(subjects) {
			return ErrInvalid
		}
		if !CanChangeReaders(p) {
			return ErrConflict
		}
		after := slices.Clone(subjects)
		slices.Sort(after)
		if slices.Equal(after, p.ReadSubjects) {
			return ErrInvalid
		}
		c = &ReaderChange{State: "preview", Generation: p.Generation, PreviewRevision: revision + 1, Before: slices.Clone(p.ReadSubjects), After: after, Tables: slices.Clone(p.Tables)}
		c.ID = readerChangeID(p, *c)
	case "approve":
		if !canApproveReaders(p) || approval != c.ID {
			return ErrConflict
		}
		copy := *c
		copy.State = "approved"
		c = &copy
	case "discard":
		if c == nil || c.State != "preview" {
			return ErrConflict
		}
		c = nil
	default:
		return ErrInvalid
	}
	encoded := []byte("{}")
	if c != nil {
		encoded, _ = json.Marshal(c)
	}
	n, err := s.q.PipelineReaders(ctx, s.pool, sqlc.PipelineReadersParams{AccountID: account, ID: id, Revision: revision, ReaderChange: encoded})
	if err != nil {
		return storeError(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

// A policy-only acknowledgement preserves sync observations and checkpoints.
// The next ordinary tick observes source health using the new applied readers.
func (s *PostgresStore) finishReaders(ctx context.Context, p Pipeline, c *ReaderChange) error {
	if c == nil || c.State != "applied" {
		return ErrInvalid
	}
	n, err := s.q.PipelineFinishReaders(ctx, s.pool, sqlc.PipelineFinishReadersParams{AccountID: p.AccountID, ID: p.ID, Revision: p.Revision, LeaseToken: p.LeaseToken, ChangeID: c.ID})
	if err != nil {
		return storeError(err)
	}
	if n != 1 {
		_ = s.q.PipelineRelease(ctx, s.pool, sqlc.PipelineReleaseParams{ID: p.ID, LeaseToken: p.LeaseToken})
		return ErrConflict
	}
	return nil
}
