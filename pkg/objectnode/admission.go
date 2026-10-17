package objectnode

import (
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrWritePressure = errors.New("object node: new writes are throttled by storage pressure")

type WriteAdmissionOptions struct {
	Enabled                                        bool
	LagHigh, LagLow, RecordsHigh, RecordsLow       int64
	JournalHigh, JournalLow, ReplayHigh, ReplayLow int64
	MinFreeBytes, ResumeFreeBytes                  int64
}

func DefaultWriteAdmissionOptions() WriteAdmissionOptions {
	return WriteAdmissionOptions{Enabled: true, LagHigh: api.ObjectNodeAdmissionLagHigh, LagLow: api.ObjectNodeAdmissionLagLow,
		RecordsHigh: api.ObjectNodeAdmissionRecordsHigh, RecordsLow: api.ObjectNodeAdmissionRecordsLow,
		JournalHigh: api.ObjectNodeAdmissionJournalHigh, JournalLow: api.ObjectNodeAdmissionJournalLow,
		ReplayHigh: api.ObjectNodeAdmissionReplayHigh, ReplayLow: api.ObjectNodeAdmissionReplayLow,
		MinFreeBytes: api.ObjectUploadSpoolMinFreeBytes, ResumeFreeBytes: api.ObjectUploadSpoolMinFreeBytes + api.ObjectNodeAdmissionFreeRecovery}
}

func (o WriteAdmissionOptions) Validate() error {
	for _, bound := range [][3]int64{{o.LagLow, o.LagHigh, api.ObjectNodeReplicationMaxEntries}, {o.RecordsLow, o.RecordsHigh, api.ObjectNodeReplicationMaxEntries}, {o.JournalLow, o.JournalHigh, api.ObjectNodeReplicationJournalBytes}, {o.ReplayLow, o.ReplayHigh, api.ObjectNodeReplicationBlobBytes}} {
		if bound[0] < 0 || bound[0] >= bound[1] || bound[1] > bound[2] {
			return ErrInvalid
		}
	}
	if o.MinFreeBytes < 0 || o.ResumeFreeBytes <= o.MinFreeBytes || o.ResumeFreeBytes > api.MaxObjectStoragePolicyValue {
		return ErrInvalid
	}
	return nil
}

type admissionCost struct{ records, journal, blobs, replay, disk int64 }

func (c *admissionCost) add(other admissionCost, sign int64) {
	c.records += sign * other.records
	c.journal += sign * other.journal
	c.blobs += sign * other.blobs
	c.replay += sign * other.replay
	c.disk += sign * other.disk
}

type WriteAdmissionStatus struct {
	Enabled             bool   `json:"enabled"`
	Blocked             bool   `json:"blocked"`
	Reasons             uint64 `json:"reasons"`
	Active              int64  `json:"active"`
	Accepted            int64  `json:"accepted_total"`
	Rejected            int64  `json:"rejected_total"`
	ReservedRecords     int64  `json:"reserved_records"`
	ReservedReplayBytes int64  `json:"reserved_replay_bytes"`
	ReservedDiskBytes   int64  `json:"reserved_disk_bytes"`
}

type writeAdmission struct {
	options   WriteAdmissionOptions
	status    WriteAdmissionStatus
	reserved  admissionCost
	available func() (uint64, error)
}

const (
	AdmissionLag uint64 = 1 << iota
	AdmissionRecords
	AdmissionJournal
	AdmissionReplay
	AdmissionFree
	AdmissionDiskProbe
)

// ConfigureWriteAdmission is process-local policy, configured before serving.
// It never changes the durable format, retention pins or an existing decision.
func (s *Store) ConfigureWriteAdmission(o WriteAdmissionOptions) error {
	if err := o.Validate(); err != nil {
		return err
	}
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if s.admission != nil && s.admission.status.Active != 0 {
		return ErrCapacity
	}
	s.admission = &writeAdmission{options: o, status: WriteAdmissionStatus{Enabled: o.Enabled}, available: s.AvailableBytes}
	return nil
}

// AdmissionStatus is a cached observation; scraping it performs no I/O.
func (s *Store) AdmissionStatus() WriteAdmissionStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.admission == nil {
		return WriteAdmissionStatus{}
	}
	return s.admission.snapshot()
}

func (a *writeAdmission) snapshot() WriteAdmissionStatus {
	r := a.status
	r.ReservedRecords, r.ReservedReplayBytes, r.ReservedDiskBytes = a.reserved.records, a.reserved.replay, a.reserved.disk
	return r
}

func admissionRequestCost(kind string, size int64) (admissionCost, error) {
	if size < 0 || size > api.MaxObjectUploadBytes {
		return admissionCost{}, ErrInvalid
	}
	c := admissionCost{records: 1}
	switch kind {
	case "put":
		c.records = 3
		c.blobs = 1
	case "part":
		c.blobs = 1
	case "complete":
		c.records = api.ObjectNodeAdmissionCompletionRecords
		c.blobs = 1
	case "create", "bucket":
		if size != 0 {
			return c, ErrInvalid
		}
	default:
		return c, ErrInvalid
	}
	c.journal = c.records * api.ObjectNodeReplicationRecordMaxBytes
	c.replay = size
	c.disk = api.ObjectNodeAdmissionPhysicalCopies*size + c.journal
	return c, nil
}

// CheckWriteAdmission is only a fresh, read-only hint. No slot or write outcome
// is promised; actual operations reserve again under the catalog lock.
func (s *Store) CheckWriteAdmission(kind string, size int64) error {
	cost, err := admissionRequestCost(kind, size)
	if err != nil {
		return err
	}
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.servingError(); err != nil {
		return err
	}
	return s.checkAdmissionLocked(cost)
}

func (s *Store) checkAdmissionLocked(cost admissionCost) error {
	a := s.admission
	if a == nil || !a.options.Enabled {
		return nil
	}
	if s.replicationDirty || s.authorityDirty {
		return ErrCorrupt
	}
	o := a.options
	current := a.reserved
	current.add(cost, 1)
	lag, records, journal, replay := int64(0), int64(0), int64(0), int64(0)
	if s.replication != nil {
		if len(s.replication.Followers) > 0 || s.replication.Durability != nil && s.replication.Durability.Mode == DurabilityReplica {
			lag = s.replication.AppliedSequence - retentionSafeSequence(s.replication.ReplicationStatus) + current.records
		}
		records = max(s.journalUsage.Records+current.records, s.journalUsage.Blobs+current.blobs)
		journal, replay = s.journalUsage.JournalBytes+current.journal, s.journalUsage.BlobBytes+current.replay
	}
	for _, v := range []struct {
		bit              uint64
		value, high, low int64
	}{{AdmissionLag, lag, o.LagHigh, o.LagLow}, {AdmissionRecords, records, o.RecordsHigh, o.RecordsLow}, {AdmissionJournal, journal, o.JournalHigh, o.JournalLow}, {AdmissionReplay, replay, o.ReplayHigh, o.ReplayLow}} {
		a.latch(v.bit, v.value >= v.high, v.value > v.low)
	}
	free, err := a.available()
	a.latch(AdmissionDiskProbe, err != nil, false)
	if err == nil {
		need := uint64(current.disk)
		a.latch(AdmissionFree, free < need || free-need < uint64(o.MinFreeBytes), free < need || free-need < uint64(o.ResumeFreeBytes))
	}
	a.status.Blocked = a.status.Reasons != 0
	if a.status.Blocked {
		return fmt.Errorf("%w (reason mask %d)", ErrWritePressure, a.status.Reasons)
	}
	return nil
}

func (a *writeAdmission) latch(bit uint64, high, keep bool) {
	if high || a.status.Reasons&bit != 0 && keep {
		a.status.Reasons |= bit
	} else {
		a.status.Reasons &^= bit
	}
}

// Existing receipts/frozen completions can finish without a new admission.
// Their work still counts against any concurrent new admission's headroom.
func (s *Store) admitWriteLocked(kind string, size int64, existing bool) (*admissionCost, error) {
	if s.admission == nil || !s.admission.options.Enabled {
		return nil, nil
	}
	cost, err := admissionRequestCost(kind, size)
	if err != nil {
		return nil, err
	}
	if !existing {
		if err := s.checkAdmissionLocked(cost); err != nil {
			s.admission.status.Rejected++
			return nil, err
		}
	}
	s.admission.reserved.add(cost, 1)
	s.admission.status.Active++
	s.admission.status.Accepted++
	return &cost, nil
}

func (s *Store) releaseAdmissionLocked(cost *admissionCost) {
	if cost == nil {
		return
	}
	s.admission.reserved.add(*cost, -1)
	s.admission.status.Active--
}

func (s *Store) releaseAdmission(cost *admissionCost) {
	if cost == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releaseAdmissionLocked(cost)
}

func (s *Store) beginAdmission(kind string, size int64, receipt string) (*admissionCost, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.admission == nil || !s.admission.options.Enabled {
		return nil, nil
	}
	if err := s.servingError(); err != nil {
		return nil, err
	}
	// Recovery of an older durable decision precedes admission of new work.
	// Receipt queries and cleanup also retain their independent recovery paths.
	if err := s.finishPending(false); err != nil {
		return nil, err
	}
	existing := false
	if receipt != "" {
		_, err := s.readReceipt(receipt)
		if err != nil && !errors.Is(err, ErrNoReceipt) {
			return nil, err
		}
		existing = err == nil
	}
	return s.admitWriteLocked(kind, size, existing)
}
