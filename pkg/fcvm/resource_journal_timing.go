package fcvm

import (
	"sync"
	"time"
)

// journalMutex is the resource journal's lock. It remembers how long its
// current holder waited for it, because every record commit runs under the
// lock and a wake's staging waits behind other wakes' fsyncs.
type journalMutex struct {
	sync.Mutex
	waited time.Duration // holder's wait; valid only while held
}

func (m *journalMutex) Lock() {
	if m.TryLock() {
		m.waited = 0
		return
	}
	start := time.Now()
	m.Mutex.Lock()
	m.waited = time.Since(start)
}

// takeWait returns the holder's wait once, so a holder that commits several
// records counts it a single time. Call it with the lock held.
func (m *journalMutex) takeWait() time.Duration {
	w := m.waited
	m.waited = 0
	return w
}

// resourceJournalTiming is the journal's cost for one instance since it was
// last taken: record commits, the fsyncs inside them (record file and
// directory), and the time those commits waited for the journal lock.
// Production-us bpftrace saw ~60 fsyncs, ~85 ms, inside one restore's staging;
// this lets vmmd report that share without bpftrace.
type resourceJournalTiming struct {
	Commits  int
	Sync     time.Duration
	LockWait time.Duration
}

// timingKey is the wake a record write is for. A prepared network handed to
// an instance is still keyed by its spare until adoption rewrites the lease,
// but the transfer runs on the target's wake.
func (r resourceJournalRecord) timingKey() string {
	if r.Prepared != nil && r.Prepared.Target != "" {
		return r.Prepared.Target
	}
	return r.Lease.Instance
}

// noteCommit starts accounting one record commit for key. Call it with the
// lock held.
func (j *ResourceJournal) noteCommit(key string) {
	if j.timing == nil {
		j.timing = make(map[string]resourceJournalTiming)
	}
	t := j.timing[key]
	t.Commits++
	t.LockWait += j.mu.takeWait()
	j.timing[key] = t
}

// timedSync runs one fsync of a commit for key and adds its duration. Call
// it with the lock held, after noteCommit.
func (j *ResourceJournal) timedSync(key string, sync func() error) error {
	start := time.Now()
	err := sync()
	t := j.timing[key]
	t.Sync += time.Since(start)
	j.timing[key] = t
	return err
}

// takeTiming returns and clears key's journal cost. Safe on a nil journal.
func (j *ResourceJournal) takeTiming(key string) resourceJournalTiming {
	if j == nil {
		return resourceJournalTiming{}
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	t := j.timing[key]
	delete(j.timing, key)
	return t
}
