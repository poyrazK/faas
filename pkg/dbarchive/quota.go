package dbarchive

import (
	"io"
	"math"
	"sync"
)

// quotaState counts bytes in uncommitted writes, which LocalBytes cannot
// see. Its lock covers checking committed usage and reserving more space.
type quotaState struct {
	mu      sync.Mutex
	pending int64
	users   int // protected by Server.quotaMu
}

type archiveQuota struct {
	server *Server
	header Header
	state  *quotaState
	held   int64
	done   sync.Once
}

func (s *Server) beginQuota(header Header) *archiveQuota {
	q := &archiveQuota{server: s, header: header}
	if header.QuotaBytes == 0 || s.Retention == nil {
		return q
	}
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	if s.quotas == nil {
		s.quotas = map[string]*quotaState{}
	}
	state := s.quotas[header.DatabaseID]
	if state == nil {
		state = &quotaState{}
		s.quotas[header.DatabaseID] = state
	}
	state.users++
	q.state = state
	return q
}

// reserve keeps bytes charged until the writer commits or removes its
// temporary files. Backup writers reserve their actual compressed output,
// leaving WAL free to progress while pg_basebackup waits for archiving.
func (q *archiveQuota) reserve(size int64) error {
	if q == nil || q.state == nil {
		return nil
	}
	q.state.mu.Lock()
	defer q.state.mu.Unlock()
	room, err := q.roomLocked()
	if err != nil {
		return err
	}
	if size < 0 || size > room {
		return ErrQuotaExceeded
	}
	q.state.pending += size
	q.held += size
	return nil
}

func (q *archiveQuota) roomLocked() (int64, error) {
	used, err := q.server.Retention.LocalBytes(q.header.DatabaseID)
	if err != nil {
		return 0, err
	}
	return q.header.QuotaBytes - used - q.state.pending, nil
}

func (q *archiveQuota) room() (int64, error) {
	if q.state == nil {
		return math.MaxInt64, nil
	}
	q.state.mu.Lock()
	defer q.state.mu.Unlock()
	return q.roomLocked()
}

func (q *archiveQuota) release() {
	q.done.Do(func() {
		if q.state == nil {
			return
		}
		q.state.mu.Lock()
		q.state.pending -= q.held
		q.held = 0
		q.state.mu.Unlock()
		q.server.quotaMu.Lock()
		defer q.server.quotaMu.Unlock()
		q.state.users--
		if q.state.users == 0 {
			delete(q.server.quotas, q.header.DatabaseID)
		}
	})
}

type quotaWriter struct {
	io.Writer
	quota *archiveQuota
}

func (w quotaWriter) Write(p []byte) (int, error) {
	if err := w.quota.reserve(int64(len(p))); err != nil {
		return 0, err
	}
	return w.Writer.Write(p)
}
