package objectnode

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
)

func configureAdmission(t *testing.T, s *Store, change func(*WriteAdmissionOptions)) {
	t.Helper()
	o := DefaultWriteAdmissionOptions()
	o.MinFreeBytes, o.ResumeFreeBytes = 1, 2
	if change != nil {
		change(&o)
	}
	if err := s.ConfigureWriteAdmission(o); err != nil {
		t.Fatal(err)
	}
}

type unreadAdmissionBody struct{ read bool }

func (r *unreadAdmissionBody) Read([]byte) (int, error) {
	r.read = true
	return 0, errors.New("rejected body was read")
}

func TestWriteAdmissionFollowerLagHysteresisAndReceiptSafety(t *testing.T) {
	s, follower := replicationPair(t)
	s = registerTestFollower(t, s, follower)
	configureAdmission(t, s, func(o *WriteAdmissionOptions) { o.LagHigh, o.LagLow = 10, 4 })
	for i := range 7 {
		putText(t, s, string(rune('a'+i)), "retained")
	}
	id := uuid.NewString()
	body := &unreadAdmissionBody{}
	if _, err := s.Put(t.Context(), "test-bucket", "blocked", body, 1, PutOptions{Receipt: id}); !errors.Is(err, ErrWritePressure) || body.read {
		t.Fatal("new write was not rejected before staging", err, body.read)
	}
	if _, err := s.Receipt("test-bucket", "blocked", id); !errors.Is(err, ErrNoReceipt) {
		t.Fatal("throttle created a receipt", err)
	}
	if entries, err := os.ReadDir(filepath.Join(s.root, "staging")); err != nil || len(entries) != 0 {
		t.Fatal("throttle left staging files", entries, err)
	}
	if _, err := s.ReportFollower(t.Context(), followerReportAt(t, s, 4)); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckWriteAdmission("put", 1); !errors.Is(err, ErrWritePressure) {
		t.Fatal("reopened above lag low watermark", err)
	}
	if _, err := s.ReportFollower(t.Context(), followerReportAt(t, s, 6)); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckWriteAdmission("put", 1); err != nil {
		t.Fatal("did not reopen at low watermark", err)
	}
	putText(t, s, "reopened", "fresh")
	if readText(t, s, "a") != "retained" {
		t.Fatal("pressure changed acknowledged bytes")
	}
	catchUpTestReplica(t, s, follower)
	if p := s.AdmissionStatus(); p.Active != 0 || p.ReservedDiskBytes != 0 || p.Rejected != 1 {
		t.Fatal("reservation leaked", p)
	}
}

func TestWriteAdmissionReplayBytesReopenAfterSafeCompaction(t *testing.T) {
	s, follower := replicationPair(t)
	s = registerTestFollower(t, s, follower)
	configureAdmission(t, s, func(o *WriteAdmissionOptions) { o.ReplayHigh, o.ReplayLow = 12<<10, 8<<10 })
	putText(t, s, "key", strings.Repeat("a", 4<<10))
	putText(t, s, "key", strings.Repeat("b", 4<<10))
	if err := s.CheckWriteAdmission("put", 4<<10); !errors.Is(err, ErrWritePressure) {
		t.Fatal("retained replay bytes did not close admission", err)
	}
	// Maintenance cannot prune an unreported follower's required history.
	for range 5 {
		if _, err := s.CompactStep(t.Context(), onlineOptions()); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CheckWriteAdmission("put", 4<<10); !errors.Is(err, ErrWritePressure) {
		t.Fatal("pin was bypassed", err)
	}
	catchUpTestReplica(t, s, follower)
	drainOnline(t, s, onlineOptions())
	if err := s.CheckWriteAdmission("put", 4<<10); err != nil {
		t.Fatal("safe compaction did not reopen admission", err)
	}
	if readText(t, s, "key") != strings.Repeat("b", 4<<10) {
		t.Fatal("compaction changed current bytes")
	}
}

func TestWriteAdmissionWithoutFollowerPinsUsesRetentionOnly(t *testing.T) {
	s, _ := replicationPair(t)
	configureAdmission(t, s, func(o *WriteAdmissionOptions) { o.LagHigh, o.LagLow = 4, 1 })
	for i := range 8 {
		putText(t, s, string(rune('a'+i)), "local journal")
	}
	status, err := s.ReplicationStatus()
	if err != nil || status.AppliedSequence <= 4 || len(status.Followers) != 0 {
		t.Fatal(status, err)
	}
	if err = s.CheckWriteAdmission("put", 1); err != nil {
		t.Fatal("unregistered history was treated as required follower lag", err)
	}
}

func TestWriteAdmissionPressureWakesCompactionBelowItsWatermarks(t *testing.T) {
	s, _ := replicationPair(t)
	putText(t, s, "overwritten", strings.Repeat("a", 2048))
	putText(t, s, "overwritten", strings.Repeat("b", 2048))
	configureAdmission(t, s, func(o *WriteAdmissionOptions) { o.ReplayHigh, o.ReplayLow = 4096, 2048 })
	if err := s.CheckWriteAdmission("put", 1); !errors.Is(err, ErrWritePressure) {
		t.Fatal(err)
	}
	// Cached admission pressure must independently trigger an attempt, even
	// when an unrelated free-space observation is unavailable. No I/O is needed
	// to choose work; CompactStep still validates and durably prunes its prefix.
	root := s.root
	s.root = filepath.Join(t.TempDir(), "missing")
	defer func() { s.root = root }()
	needed, err := s.needsCompaction(DefaultCompactionOptions())
	if err != nil || !needed {
		t.Fatal("pressure did not wake maintenance", needed, err)
	}
	s.admission.status.Reasons, s.admission.status.Blocked = 0, false
	if _, err = s.needsCompaction(DefaultCompactionOptions()); err == nil {
		t.Fatal("control did not reach the free-space probe")
	}
}

func TestWriteAdmissionDiskHysteresisAndProbeFailure(t *testing.T) {
	s := testStore(t)
	configureAdmission(t, s, func(o *WriteAdmissionOptions) { o.MinFreeBytes, o.ResumeFreeBytes = 100, 200 })
	cost, _ := admissionRequestCost("put", 1)
	free := uint64(cost.disk + 99)
	probeErr := error(nil)
	s.admission.available = func() (uint64, error) { return free, probeErr }
	if err := s.CheckWriteAdmission("put", 1); !errors.Is(err, ErrWritePressure) {
		t.Fatal(err)
	}
	free = uint64(cost.disk + 150)
	if err := s.CheckWriteAdmission("put", 1); !errors.Is(err, ErrWritePressure) {
		t.Fatal("disk latch reopened early", err)
	}
	free = uint64(cost.disk + 200)
	if err := s.CheckWriteAdmission("put", 1); err != nil {
		t.Fatal("disk latch did not reopen", err)
	}
	probeErr = errors.New("statfs failed")
	if err := s.CheckWriteAdmission("put", 1); !errors.Is(err, ErrWritePressure) || s.AdmissionStatus().Reasons&AdmissionDiskProbe == 0 {
		t.Fatal("unknown free space was admitted", err)
	}
	probeErr = nil
	if err := s.CheckWriteAdmission("put", 1); err != nil {
		t.Fatal("disk probe did not recover", err)
	}
}

func TestWriteAdmissionJournalCountsAndIndependentLatches(t *testing.T) {
	s, _ := replicationPair(t)
	configureAdmission(t, s, func(o *WriteAdmissionOptions) {
		o.RecordsHigh, o.RecordsLow = 10, 4
		o.JournalHigh, o.JournalLow = 20<<20, 10<<20
	})
	s.journalUsage.Records = 7
	if err := s.CheckWriteAdmission("put", 0); !errors.Is(err, ErrWritePressure) {
		t.Fatal(err)
	}
	// A healthy metric in its hysteresis band must not latch a new reason.
	s.journalUsage.Records = 1
	s.journalUsage.JournalBytes = 6 << 20
	if err := s.CheckWriteAdmission("put", 0); err != nil {
		t.Fatal("unrelated middle-band metric prevented reopening", err)
	}
	s.journalUsage.JournalBytes = 14 << 20
	if err := s.CheckWriteAdmission("put", 0); !errors.Is(err, ErrWritePressure) {
		t.Fatal(err)
	}
	s.journalUsage.JournalBytes = 5 << 20
	if err := s.CheckWriteAdmission("put", 0); !errors.Is(err, ErrWritePressure) {
		t.Fatal("journal latch reopened above low watermark", err)
	}
	s.journalUsage.JournalBytes = 4 << 20
	if err := s.CheckWriteAdmission("put", 0); err != nil {
		t.Fatal(err)
	}
}

type admissionHeldBody struct {
	started, resume chan struct{}
	body            io.Reader
	first           bool
}

func (r *admissionHeldBody) Read(p []byte) (int, error) {
	if !r.first {
		r.first = true
		close(r.started)
		<-r.resume
	}
	return r.body.Read(p)
}

func TestWriteAdmissionAcceptedWriteFinishesAndReleasesOnFailure(t *testing.T) {
	s := testStore(t)
	configureAdmission(t, s, nil)
	firstCost, _ := admissionRequestCost("put", 4)
	s.admission.available = func() (uint64, error) { return uint64(firstCost.disk + 2), nil }
	body := &admissionHeldBody{started: make(chan struct{}), resume: make(chan struct{}), body: strings.NewReader("data")}
	done := make(chan error, 1)
	go func() { _, err := s.Put(t.Context(), "test-bucket", "accepted", body, 4, PutOptions{}); done <- err }()
	<-body.started
	released := false
	defer func() {
		if !released {
			close(body.resume)
			<-done
		}
	}()
	if p := s.AdmissionStatus(); p.Active != 1 || p.ReservedDiskBytes != firstCost.disk {
		t.Fatal(p)
	}
	if _, err := s.Put(t.Context(), "test-bucket", "second", &unreadAdmissionBody{}, 4, PutOptions{}); !errors.Is(err, ErrWritePressure) {
		t.Fatal("concurrent reservations were ignored", err)
	}
	if err := s.ConfigureWriteAdmission(DefaultWriteAdmissionOptions()); !errors.Is(err, ErrCapacity) {
		t.Fatal("active policy was replaced", err)
	}
	close(body.resume)
	released = true
	if err := <-done; err != nil {
		t.Fatal("accepted operation was throttled during publication", err)
	}
	if p := s.AdmissionStatus(); p.Active != 0 || p.ReservedDiskBytes != 0 {
		t.Fatal(p)
	}
	// A failed staging read and a canceled request must also release admission.
	for _, cancel := range []bool{false, true} {
		ctx, stop := context.WithCancel(t.Context())
		if cancel {
			stop()
		}
		if _, err := s.Put(ctx, "test-bucket", "failed", &unreadAdmissionBody{}, 4, PutOptions{}); err == nil {
			t.Fatal("expected staging failure")
		}
		stop()
		if p := s.AdmissionStatus(); p.Active != 0 || p.ReservedRecords != 0 {
			t.Fatal("failed operation leaked admission", p)
		}
	}
}

func TestWriteAdmissionRecoversExistingReceiptBeforeRejectingNewWork(t *testing.T) {
	s := testStore(t)
	configureAdmission(t, s, nil)
	id := uuid.NewString()
	s.persist = func(path string, value any) (bool, error) {
		if strings.HasSuffix(path, "current.json") {
			return false, errors.New("disk fault")
		}
		return writeJSON(path, value)
	}
	if _, err := receiptPut(t, s, "key", "acknowledged", id, PutOptions{}); err == nil {
		t.Fatal("missing injected interruption")
	}
	s.persist = writeJSON
	s.admission.available = func() (uint64, error) { return 0, nil }
	if _, err := s.Put(t.Context(), "test-bucket", "new", &unreadAdmissionBody{}, 1, PutOptions{}); !errors.Is(err, ErrWritePressure) {
		t.Fatal(err)
	}
	proof, err := s.Receipt("test-bucket", "key", id)
	if err != nil || proof.Status != ReceiptCommitted {
		t.Fatal("pressure stranded a durable intent", proof, err)
	}
	if _, err = receiptPut(t, s, "key", "acknowledged", id, PutOptions{}); err != nil {
		t.Fatal("existing receipt replay was throttled", err)
	}
	if _, err = receiptPut(t, s, "key", "changed", id, PutOptions{}); !errors.Is(err, ErrReceiptReuse) {
		t.Fatal("receipt identity was weakened", err)
	}
}

func TestWriteAdmissionMultipartFreezeAndResume(t *testing.T) {
	s := testStore(t)
	u := newMultipart(t, s, objectchecksum.Policy{})
	p := multipartPart(t, s, u, 1, []byte("part"))
	configureAdmission(t, s, nil)
	s.admission.available = func() (uint64, error) { return 0, nil }
	if _, err := s.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, completion(p), nil); !errors.Is(err, ErrWritePressure) {
		t.Fatal(err)
	}
	stored, err := s.readMultipart(u.ID)
	if err != nil || stored.Completion != nil || stored.Status != multipartActive {
		t.Fatal("throttle froze a new completion", stored, err)
	}
	if _, err = s.UploadPart(t.Context(), u.Bucket, u.Key, u.ID, 2, &unreadAdmissionBody{}, 1, PutOptions{}); !errors.Is(err, ErrWritePressure) {
		t.Fatal(err)
	}
	if _, err = s.CreateMultipart(u.Bucket, "new", MultipartOptions{}); !errors.Is(err, ErrWritePressure) {
		t.Fatal(err)
	}
	s.admission.available = s.AvailableBytes
	interrupted := errors.New("after durable freeze")
	if _, err = s.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, completion(p), func(int64) (func(), error) { return nil, interrupted }); !errors.Is(err, interrupted) {
		t.Fatal(err)
	}
	s.admission.available = func() (uint64, error) { return 0, nil }
	if _, err = s.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, completion(p), nil); err != nil {
		t.Fatal("frozen completion was throttled", err)
	}
	if _, err = s.MultipartReceipt(u.Bucket, u.Key, u.ReceiptID); err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(u.Bucket, u.Key); err != nil {
		t.Fatal("pressure blocked cleanup", err)
	}
	if p := s.AdmissionStatus(); p.Active != 0 || p.ReservedDiskBytes != 0 {
		t.Fatal(p)
	}
}
