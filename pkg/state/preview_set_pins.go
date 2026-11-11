// adr: 20261108150000
package state

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/google/uuid"
)

// PRPreviewMemberPinStore pins preview members a PR push did not rebuild to
// the commit their live deployment was built from (ADR-20261108150000).
type PRPreviewMemberPinStore interface {
	// PinPRPreviewMembers records pins for head. It succeeds only while head
	// is the set's recorded, open head and every pinned app is a member;
	// otherwise it returns ErrNotFound and leaves the set alone.
	PinPRPreviewMembers(ctx context.Context, head PRPreviewHead, pins map[string]string) error
}

var (
	_ PRPreviewMemberPinStore = (*PgStore)(nil)
	_ PRPreviewMemberPinStore = (*MemStore)(nil)
)

func validatePRPreviewPins(head PRPreviewHead, pins map[string]string) error {
	if head.InstallationID <= 0 || head.RepoFullName == "" || head.PRNumber <= 0 || !previewSetSHA.MatchString(head.CommitSHA) {
		return fmt.Errorf("state: invalid PR preview head: %w", ErrConflict)
	}
	if len(pins) == 0 || len(pins) > 100 {
		return fmt.Errorf("state: invalid PR preview pins: %w", ErrConflict)
	}
	for id, commit := range pins {
		if _, err := uuid.Parse(id); err != nil || !previewSetSHA.MatchString(commit) || commit == head.CommitSHA {
			return fmt.Errorf("state: invalid PR preview pin %q: %w", id, ErrConflict)
		}
	}
	return nil
}

func decodePRPreviewPins(raw []byte) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var pins map[string]string
	if err := json.Unmarshal(raw, &pins); err != nil {
		return nil, fmt.Errorf("state: decode PR preview pins: %w", err)
	}
	if len(pins) == 0 {
		return nil, nil
	}
	return pins, nil
}

func (s *PgStore) PinPRPreviewMembers(ctx context.Context, head PRPreviewHead, pins map[string]string) error {
	if err := validatePRPreviewPins(head, pins); err != nil {
		return err
	}
	raw, err := json.Marshal(pins)
	if err != nil {
		return fmt.Errorf("state: encode PR preview pins: %w", err)
	}
	ids := make([]string, 0, len(pins))
	for id := range pins {
		ids = append(ids, id)
	}
	// Resolve the one set first: an unscoped head shared by two accounts is
	// ErrConflict here, never a write to both.
	set, err := s.GetPRPreviewSet(ctx, head.InstallationID, head.RepoFullName, head.PRNumber)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `update pr_preview_sets
		set member_pins = $5::jsonb, member_pins_head = $4, updated_at = now()
		where installation_id = $1 and repo_full_name = $2 and pr_number = $3
		  and commit_sha = $4 and closed_at is null and member_app_ids @> $6::text[]
		  and root_app_id = $7::uuid`,
		head.InstallationID, head.RepoFullName, head.PRNumber, head.CommitSHA, raw, ids, set.RootAppID)
	if err != nil {
		return fmt.Errorf("state: pin PR preview members: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (m *MemStore) PinPRPreviewMembers(ctx context.Context, head PRPreviewHead, pins map[string]string) error {
	if err := validatePRPreviewPins(head, pins); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key, err := m.previewSetKeyForAccountLocked(ctx, head.InstallationID, head.RepoFullName, head.PRNumber)
	if err != nil {
		return err
	}
	set := m.previewSets[key]
	if set.Closed || set.CommitSHA != head.CommitSHA {
		return ErrNotFound
	}
	copied := make(map[string]string, len(pins))
	for id, commit := range pins {
		if !slices.Contains(set.MemberAppIDs, id) {
			return ErrNotFound
		}
		copied[id] = commit
	}
	set.MemberCommitSHAs, set.pinnedHead = copied, head.CommitSHA
	m.previewSets[key] = set
	return nil
}
