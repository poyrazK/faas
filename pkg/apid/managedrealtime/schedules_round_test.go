package managedrealtime

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// roundScheduleStore serves due schedules and records their publishes.
type roundScheduleStore struct {
	mu        sync.Mutex
	due       []state.ManagedRealtimeSchedule
	published map[string][]string // channel → schedule IDs in publish order
	running   int
	peak      int
}

func (s *roundScheduleStore) PutManagedRealtimeSchedule(context.Context, state.ManagedRealtimeSchedule) (state.ManagedRealtimeSchedule, error) {
	return state.ManagedRealtimeSchedule{}, nil
}
func (s *roundScheduleStore) ListManagedRealtimeSchedules(context.Context, string, string) ([]state.ManagedRealtimeSchedule, error) {
	return nil, nil
}
func (s *roundScheduleStore) UpdateManagedRealtimeSchedule(context.Context, string, string, string, int64, *time.Time) (state.ManagedRealtimeSchedule, error) {
	return state.ManagedRealtimeSchedule{}, nil
}
func (s *roundScheduleStore) ListDueManagedRealtimeSchedules(_ context.Context, limit int) ([]state.ManagedRealtimeSchedule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]state.ManagedRealtimeSchedule(nil), s.due[:min(limit, len(s.due))]...), nil
}
func (s *roundScheduleStore) PublishManagedRealtimeSchedule(_ context.Context, row state.ManagedRealtimeSchedule) (state.ManagedRealtimeChannelMessage, error) {
	s.mu.Lock()
	s.running++
	s.peak = max(s.peak, s.running)
	s.mu.Unlock()
	time.Sleep(5 * time.Millisecond)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running--
	s.published[row.Channel] = append(s.published[row.Channel], row.ID)
	for i, d := range s.due {
		if d.ID == row.ID {
			s.due = append(s.due[:i], s.due[i+1:]...)
			break
		}
	}
	return state.ManagedRealtimeChannelMessage{EndpointID: row.EndpointID, Channel: row.Channel}, nil
}
func (s *roundScheduleStore) FailManagedRealtimeSchedule(context.Context, state.ManagedRealtimeSchedule, string) error {
	return nil
}

// adr: 1323 (amendment of 2026-11-03) — due schedules publish several
// channels at once, each channel's in due order, and a full list is followed
// at once by the next.
func TestScheduleRoundsPublishChannelsConcurrentlyInOrder(t *testing.T) {
	store := &roundScheduleStore{published: map[string][]string{}}
	for i := range 2*scheduleListBatch + 10 {
		store.due = append(store.due, state.ManagedRealtimeSchedule{ID: fmt.Sprintf("s-%04d", i), EndpointID: "ep", Channel: fmt.Sprintf("ch-%d", i%16)})
	}
	s := &Service{Host: &ticketTestHost{store: state.NewMemStore()}}
	log := slog.New(slog.DiscardHandler)
	rounds := 0
	for rounds < scheduleRoundsPerTick {
		rounds++
		if s.runScheduleRound(context.Background(), store, log) < scheduleListBatch {
			break
		}
	}
	if rounds != 3 || len(store.due) != 0 {
		t.Fatalf("rounds = %d, left due = %d; want 3 rounds and none left", rounds, len(store.due))
	}
	if store.peak < 2 || store.peak > schedulePublishWorkers {
		t.Fatalf("peak concurrent publishes = %d, want 2..%d", store.peak, schedulePublishWorkers)
	}
	for channel, ids := range store.published {
		for i := 1; i < len(ids); i++ {
			if ids[i-1] > ids[i] {
				t.Fatalf("channel %s published out of due order: %v", channel, ids)
			}
		}
	}
}
