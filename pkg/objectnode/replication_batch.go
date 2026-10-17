package objectnode

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
)

const ReplicationBatchHeader = "X-Gregale-Replication-Entry-Batch"

// Bound element allocation before decoding; a byte-bounded JSON array could
// otherwise contain far more than 32 small elements. Canonical encoding retains
// the same duplicate/unknown-field rejection as single-entry responses.
func decodeReplicationBatch(body []byte, entries *[]JournalEntry) error {
	d := json.NewDecoder(bytes.NewReader(body))
	token, err := d.Token()
	if err != nil || token != json.Delim('[') {
		return ErrCorrupt
	}
	for d.More() {
		if len(*entries) == api.ObjectNodeReplicationBatch {
			return ErrCapacity
		}
		var e JournalEntry
		if err = d.Decode(&e); err != nil {
			return ErrCorrupt
		}
		*entries = append(*entries, e)
	}
	if _, err = d.Token(); err != nil {
		return ErrCorrupt
	}
	canonical, err := json.Marshal(entries)
	if err != nil || !bytes.Equal(body, canonical) {
		return ErrCorrupt
	}
	return nil
}

// ReplicationBatchSource optionally fetches a bounded contiguous journal prefix.
// Prefetched records confer no received/applied position or confirmation.
type ReplicationBatchSource interface {
	EntryBatchLimit() int
	Entries(context.Context, string, int64, int) ([]JournalEntry, error)
}

// ReplicationEntries takes one source catalog read lock for a bounded page.
// Existing single-entry readers and older peers remain supported.
func (s *Store) ReplicationEntries(ctx context.Context, stream string, sequence int64, limit int) ([]JournalEntry, error) {
	if limit < 1 || limit > api.ObjectNodeReplicationBatch {
		return nil, ErrInvalid
	}
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.checkReplicationEntryPosition(stream, sequence); err != nil {
		return nil, err
	}
	entries := make([]JournalEntry, 0, limit)
	bytes := 2 // Array brackets; every additional record also needs a comma.
	for len(entries) < limit && sequence <= s.replication.AppliedSequence {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		e, err := s.readJournal(sequence)
		if err != nil {
			return nil, err
		}
		body, err := json.Marshal(e)
		if err != nil {
			return nil, err
		}
		if bytes+len(body)+1 > api.ObjectNodeReplicationBatchMaxBytes {
			break
		}
		entries = append(entries, e)
		bytes += len(body) + 1
		sequence++
	}
	return entries, nil
}

func (s *Store) fetchReplicaEntries(ctx context.Context, source ReplicationSource, head ReplicationStatus, limit int) ([]JournalEntry, error) {
	sequence := s.replication.AppliedSequence + 1
	batch, ok := source.(ReplicationBatchSource)
	if !ok {
		limit = 1
	} else {
		advertised := batch.EntryBatchLimit()
		if advertised < 1 || advertised > api.ObjectNodeReplicationBatch {
			return nil, ErrInvalid
		}
		limit = min(limit, advertised)
	}
	// Reserve before fetching, including records not applied if a step stops
	// on its byte budget. Conservative charging survives retries and steps.
	if err := s.replicaWork.waitEntries(ctx, limit); err != nil {
		return nil, err
	}
	var entries []JournalEntry
	var err error
	if ok {
		entries, err = batch.Entries(ctx, head.StreamID, sequence, limit)
	} else {
		var e JournalEntry
		e, err = source.Entry(ctx, head.StreamID, sequence)
		entries = []JournalEntry{e}
	}
	if err != nil {
		return nil, err
	}
	if !validReplicaBatch(entries, head, sequence, s.replication.AppliedHash, limit) {
		return nil, ErrReplicationFork
	}
	return entries, nil
}

func validReplicaBatch(entries []JournalEntry, head ReplicationStatus, sequence int64, previous string, limit int) bool {
	if len(entries) < 1 || len(entries) > limit {
		return false
	}
	bytes := 2
	for _, e := range entries {
		if !validJournalEntry(e) || journalEpoch(e) != head.Epoch || e.StreamID != head.StreamID || e.Sequence != sequence || e.Previous != previous || sequence > head.AppliedSequence || sequence == head.AppliedSequence && e.Hash != head.AppliedHash {
			return false
		}
		body, err := json.Marshal(e)
		if err != nil || bytes+len(body)+1 > api.ObjectNodeReplicationBatchMaxBytes {
			return false
		}
		bytes += len(body) + 1
		sequence++
		previous = e.Hash
	}
	return true
}
