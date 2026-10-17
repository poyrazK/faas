package objectnode

import (
	"context"
	"errors"
	"testing"
	"time"
)

type blockedProfileSource struct {
	localReplicationSource
	entered chan struct{}
}

func (s blockedProfileSource) Entry(ctx context.Context, _ string, _ int64) (JournalEntry, error) {
	close(s.entered)
	<-ctx.Done()
	return JournalEntry{}, ctx.Err()
}

// ADR-1558: failed attempts must retain timing without becoming applied proof.
func TestReplicationProfileIncludesFailedFetch(t *testing.T) {
	primary, directory := replicationPair(t)
	putText(t, primary, "key", "data")
	follower, err := OpenFollower(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer follower.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered := make(chan struct{})
	type result struct {
		progress ReplicationProgress
		err      error
	}
	done := make(chan result, 1)
	go func() {
		p, err := follower.CatchUp(ctx, blockedProfileSource{localReplicationSource{primary}, entered}, pacedTestOptions())
		done <- result{p, err}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("fetch phase was never entered")
	}
	cancel()
	r := <-done
	p, err := r.progress, r.err
	if !errors.Is(err, context.Canceled) || p.EntriesApplied != 0 || p.Local.AppliedSequence != 0 || p.ConfirmedSequence != nil || p.Profile.EntryFetchMS <= 0 || p.Profile.TotalMS < p.Profile.EntryFetchMS {
		t.Fatal("failed fetch lost its timing or became durable proof", p, err)
	}
	p, err = follower.CatchUp(t.Context(), localReplicationSource{primary}, pacedTestOptions())
	if err != nil || !p.CaughtUp || p.EntriesApplied != 1 || p.Profile.ApplyMS <= 0 || p.Profile.CheckpointMS <= 0 || p.Profile.JournalMS <= 0 || p.Profile.VerificationMS <= 0 || p.Profile.DownloadMS < p.Profile.VerificationMS {
		t.Fatal("resume lost its durable phase observations", p, err)
	}
	phases := p.Profile.SourceCheckMS + p.Profile.EntryFetchMS + p.Profile.JournalMS + p.Profile.DownloadMS + p.Profile.ApplyMS + p.Profile.CheckpointMS + p.Profile.ReportMS
	if phases > p.Profile.TotalMS || p.Profile.ByteWaitMS < 0 || p.Profile.EntryWaitMS < 0 {
		t.Fatal("invalid or overlapping top-level phases", p.Profile)
	}
}
