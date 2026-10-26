package state

import (
	"context"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ObjectStorageTimelineStore holds the PostgreSQL timeline an operator last
// accepted for serving object storage (ADR-20261025111015 amendment). A
// restore recovers from the WAL archive and promotes onto a new timeline,
// so the S3 gateway and public reads, which serve only while the database
// runs on the accepted timeline, stop until someone has reconciled the
// restored catalog and accepts the new one.
type ObjectStorageTimelineStore interface {
	// ObjectStorageTimelines returns the accepted timeline, "" before the
	// first acceptance, and the one the database runs on.
	ObjectStorageTimelines(ctx context.Context) (accepted, current string, err error)
	// AcceptObjectStorageTimeline accepts the current timeline and returns it.
	AcceptObjectStorageTimeline(ctx context.Context, acceptedBy string) (string, error)
	// BootstrapObjectStorageTimeline accepts the current timeline if none
	// has been accepted yet.
	BootstrapObjectStorageTimeline(ctx context.Context) error
}

var (
	_ ObjectStorageTimelineStore = (*PgStore)(nil)
	_ ObjectStorageTimelineStore = (*EdgeStore)(nil)
	_ ObjectStorageTimelineStore = (*MemStore)(nil)
)

func (s *pgCore) ObjectStorageTimelines(ctx context.Context) (string, string, error) {
	row, err := sqlc.New().ObjectStorageTimelines(ctx, s.pool)
	if err != nil {
		return "", "", fmt.Errorf("state: read object storage timelines: %w", err)
	}
	return row.Accepted, row.Current, nil
}

func (s *pgCore) AcceptObjectStorageTimeline(ctx context.Context, acceptedBy string) (string, error) {
	acceptedBy = strings.TrimSpace(acceptedBy)
	if acceptedBy == "" || len(acceptedBy) > 128 {
		return "", ErrInvalidArgument
	}
	timeline, err := sqlc.New().AcceptObjectStorageTimeline(ctx, s.pool, acceptedBy)
	if err != nil {
		return "", fmt.Errorf("state: accept object storage timeline: %w", err)
	}
	return timeline, nil
}

func (s *pgCore) BootstrapObjectStorageTimeline(ctx context.Context) error {
	if err := sqlc.New().BootstrapObjectStorageTimeline(ctx, s.pool); err != nil {
		return fmt.Errorf("state: bootstrap object storage timeline: %w", err)
	}
	return nil
}

// memObjectStorageTimeline is the MemStore's database timeline before a
// test moves it with SetObjectStorageTimelineForTest.
const memObjectStorageTimeline = "00000001"

func (m *MemStore) currentObjectStorageTimelineLocked() string {
	if m.objectStorageTimeline == "" {
		return memObjectStorageTimeline
	}
	return m.objectStorageTimeline
}

func (m *MemStore) ObjectStorageTimelines(_ context.Context) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.acceptedObjectStorageTimeline, m.currentObjectStorageTimelineLocked(), nil
}

func (m *MemStore) AcceptObjectStorageTimeline(_ context.Context, acceptedBy string) (string, error) {
	acceptedBy = strings.TrimSpace(acceptedBy)
	if acceptedBy == "" || len(acceptedBy) > 128 {
		return "", ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.acceptedObjectStorageTimeline = m.currentObjectStorageTimelineLocked()
	return m.acceptedObjectStorageTimeline, nil
}

func (m *MemStore) BootstrapObjectStorageTimeline(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.acceptedObjectStorageTimeline == "" {
		m.acceptedObjectStorageTimeline = m.currentObjectStorageTimelineLocked()
	}
	return nil
}

// SetObjectStorageTimelineForTest moves the MemStore's database onto
// another timeline, as a restore that promoted would.
func (m *MemStore) SetObjectStorageTimelineForTest(timeline string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objectStorageTimeline = timeline
}
