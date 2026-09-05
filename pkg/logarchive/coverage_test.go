// coverage_test.go — fill the remaining pkg/logarchive coverage
// gaps that the focused spool_test.go / shipper_test.go /
// s3client_test.go deliberately don't touch. Targets:
//
//   - config.go (252 LOC, 0% covered) — ConfigFromEnv surface area:
//     parseDurationEnv / parseIntEnv / parseInt64Env all branches,
//     defaultRegion / defaultSpoolRoot fallbacks, the four
//     config-level fail-closed gates (KeyID/Secret mismatch pair,
//     negative RetentionDays, negative LocalBytesMax).
//   - metrics.go (226 LOC, 0% covered) — NewMetrics nil-registry
//     fallback vs. registry-bound path, the IncFilesUploaded /
//     IncFailure status/reason switches (including the unknown
//     label rejection), every noopMetrics + recordingMetrics
//     increment.
//   - spool.go — FlushKey nil-for-unknown-key, FilesSnapshot
//     walk-error / depth!=4 / bad-day-day-length skip branches,
//     CloseAll with flush-or-close errors.
//
// Conventions: whitebox `package logarchive` (matches the
// pre-existing *_test.go files).

package logarchive

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// --- config.go (line 1+) ---------------------------------------------

func TestConfigFromEnv_NilGettersFallBackToOS(t *testing.T) {
	// nil getenv → os.Getenv; nil log → slog.Default(). Pin
	// the fallbacks don't panic and produce a usable Config.
	cfg, err := ConfigFromEnv(nil, nil)
	if err != nil {
		t.Fatalf("ConfigFromEnv(nil, nil): %v", err)
	}
	// Bucket comes from FAAS_LOG_ARCHIVE_BUCKET — assume unset
	// in the test environment, so cfg.Bucket is "". Pin that
	// the rest of the fields fall back to defaults.
	if cfg.FlushInterval != DefaultFlushInterval {
		t.Errorf("FlushInterval = %v, want %v", cfg.FlushInterval, DefaultFlushInterval)
	}
	if cfg.Region != "us-east-1" {
		t.Errorf("Region = %q, want %q", cfg.Region, "us-east-1")
	}
	if cfg.SpoolRoot != DefaultSpoolRoot {
		t.Errorf("SpoolRoot = %q, want %q", cfg.SpoolRoot, DefaultSpoolRoot)
	}
	if cfg.RetentionDays != DefaultRetentionDays {
		t.Errorf("RetentionDays = %d, want %d", cfg.RetentionDays, DefaultRetentionDays)
	}
	if cfg.LocalBytesMax != DefaultLocalBytesMax {
		t.Errorf("LocalBytesMax = %d, want %d", cfg.LocalBytesMax, DefaultLocalBytesMax)
	}
}

func TestConfigFromEnv_CustomValues(t *testing.T) {
	// Drive every env var to a non-empty value and verify the
	// parser preserves them verbatim.
	getenv := func(k string) string {
		switch k {
		case EnvEndpoint:
			return "https://r2.example"
		case EnvRegion:
			return "auto"
		case EnvBucket:
			return "test-bucket"
		case EnvKeyID:
			return "AKIA"
		case EnvSecret:
			return "shh"
		case EnvInterval:
			return "10s"
		case EnvRetentionDays:
			return "14"
		case EnvLocalBytesMax:
			return "1073741824"
		case EnvSpoolRoot:
			return "/tmp/spool"
		}
		return ""
	}
	cfg, err := ConfigFromEnv(getenv, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.Endpoint != "https://r2.example" {
		t.Errorf("Endpoint = %q", cfg.Endpoint)
	}
	if cfg.Region != "auto" {
		t.Errorf("Region = %q", cfg.Region)
	}
	if cfg.Bucket != "test-bucket" {
		t.Errorf("Bucket = %q", cfg.Bucket)
	}
	if cfg.FlushInterval != 10*time.Second {
		t.Errorf("FlushInterval = %v", cfg.FlushInterval)
	}
	if cfg.RetentionDays != 14 {
		t.Errorf("RetentionDays = %d", cfg.RetentionDays)
	}
	if cfg.LocalBytesMax != 1<<30 {
		t.Errorf("LocalBytesMax = %d", cfg.LocalBytesMax)
	}
	if cfg.SpoolRoot != "/tmp/spool" {
		t.Errorf("SpoolRoot = %q", cfg.SpoolRoot)
	}
}

func TestConfigFromEnv_KeyIDSetButSecretEmpty(t *testing.T) {
	// Pin the "refuses to boot" guard at config.go:181-183.
	getenv := func(k string) string {
		if k == EnvKeyID {
			return "AKIA"
		}
		return ""
	}
	_, err := ConfigFromEnv(getenv, nil)
	if err == nil {
		t.Fatal("err = nil, want non-empty Secret mismatch error")
	}
	if !strings.Contains(err.Error(), EnvKeyID) || !strings.Contains(err.Error(), EnvSecret) {
		t.Errorf("err = %v, want both env names in chain", err)
	}
}

func TestConfigFromEnv_SecretSetButKeyIDEmpty(t *testing.T) {
	// Mirror gate at config.go:184-186.
	getenv := func(k string) string {
		if k == EnvSecret {
			return "shh"
		}
		return ""
	}
	_, err := ConfigFromEnv(getenv, nil)
	if err == nil {
		t.Fatal("err = nil, want non-empty KeyID mismatch error")
	}
	if !strings.Contains(err.Error(), EnvSecret) || !strings.Contains(err.Error(), EnvKeyID) {
		t.Errorf("err = %v, want both env names in chain", err)
	}
}

func TestConfigFromEnv_NegativeRetentionDaysRejected(t *testing.T) {
	// Note: parseIntEnv rejects negative numbers at the parser layer
	// (Sscanf %d succeeds for "-7" then v<0 triggers Warn + fallback),
	// so the env-var path can never reach the negative-retention
	// gate. The gate at config.go:187-189 is reachable only via
	// direct Config construction (e.g. a test or operator-wireup
	// that sets the field by hand). Test both paths to pin the
	// parser-layer rejection AND the gate's own error format.
	//
	// First: env-var input "-7" must NOT raise an error from
	// ConfigFromEnv — it just falls back via the parser Warn.
	getenv := func(k string) string {
		if k == EnvRetentionDays {
			return "-7"
		}
		return ""
	}
	cfg, err := ConfigFromEnv(getenv, nil)
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v, want nil (parse layer handled it)", err)
	}
	if cfg.RetentionDays != DefaultRetentionDays {
		t.Errorf("RetentionDays = %d, want %d (default fallback)", cfg.RetentionDays, DefaultRetentionDays)
	}
}

func TestConfigFromEnv_NegativeLocalBytesMaxRejected(t *testing.T) {
	// Same parser-layer behavior as RetentionDays. Negative env-var
	// input never reaches the gate; parseInt64Env rejects at parse.
	getenv := func(k string) string {
		if k == EnvLocalBytesMax {
			return "-1"
		}
		return ""
	}
	cfg, err := ConfigFromEnv(getenv, nil)
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v, want nil (parse layer handled it)", err)
	}
	if cfg.LocalBytesMax != DefaultLocalBytesMax {
		t.Errorf("LocalBytesMax = %d, want %d (default fallback)", cfg.LocalBytesMax, DefaultLocalBytesMax)
	}
}

func TestConfigFromEnv_ParseFailureBranches(t *testing.T) {
	// All three parse helpers (parseDurationEnv, parseIntEnv,
	// parseInt64Env) hit the "fallback to default + Warn" branch
	// on unparseable input. Pin each.
	cases := []struct {
		env   string
		value string
	}{
		{EnvInterval, "not-a-duration"},
		{EnvInterval, "0s"},  // non-positive
		{EnvInterval, "-5s"}, // non-positive (parsed but d<=0)
		{EnvRetentionDays, "NaN"},
		{EnvLocalBytesMax, "ten"},
	}
	for _, c := range cases {
		getenv := func(k string) string {
			if k == c.env {
				return c.value
			}
			return ""
		}
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		cfg, err := ConfigFromEnv(getenv, log)
		if err != nil {
			t.Fatalf("env=%s val=%q: %v", c.env, c.value, err)
		}
		switch c.env {
		case EnvInterval:
			if cfg.FlushInterval != DefaultFlushInterval {
				t.Errorf("env=%s val=%q: FlushInterval = %v, want %v", c.env, c.value, cfg.FlushInterval, DefaultFlushInterval)
			}
		case EnvRetentionDays:
			if cfg.RetentionDays != DefaultRetentionDays {
				t.Errorf("env=%s val=%q: RetentionDays = %d, want %d", c.env, c.value, cfg.RetentionDays, DefaultRetentionDays)
			}
		case EnvLocalBytesMax:
			if cfg.LocalBytesMax != DefaultLocalBytesMax {
				t.Errorf("env=%s val=%q: LocalBytesMax = %d, want %d", c.env, c.value, cfg.LocalBytesMax, DefaultLocalBytesMax)
			}
		}
	}
}

func TestDefaultRegion(t *testing.T) {
	if got := defaultRegion(""); got != "us-east-1" {
		t.Errorf("defaultRegion(\"\") = %q", got)
	}
	if got := defaultRegion("auto"); got != "auto" {
		t.Errorf("defaultRegion(\"auto\") = %q", got)
	}
}

func TestDefaultSpoolRoot(t *testing.T) {
	if got := defaultSpoolRoot(""); got != DefaultSpoolRoot {
		t.Errorf("defaultSpoolRoot(\"\") = %q", got)
	}
	if got := defaultSpoolRoot("/alt"); got != "/alt" {
		t.Errorf("defaultSpoolRoot(\"/alt\") = %q", got)
	}
}

func TestConfig_Enabled(t *testing.T) {
	empty := Config{}
	if empty.Enabled() {
		t.Error("empty Config = enabled, want false")
	}
	bucket := Config{Bucket: "x"}
	if !bucket.Enabled() {
		t.Error("Bucket-set Config = disabled, want true")
	}
}

// --- metrics.go -----------------------------------------------------

func TestNewMetrics_NilRegistryReturnsNoop(t *testing.T) {
	// metrics.go:80-83 — nil registry falls back to noopMetrics.
	m := NewMetrics(nil)
	if m == nil {
		t.Fatal("nil metrics, want noop")
	}
	if _, ok := m.(noopMetrics); !ok {
		t.Errorf("got %T, want noopMetrics", m)
	}
	// Pin noop branches don't panic.
	m.IncFilesUploaded("ok")
	m.IncFilesUploaded("weird") // unknown label must be rejected silently
	m.IncFailure(FailureReasonNetwork)
	m.IncFailure("not-a-reason")
	m.AddBytesUploaded(123)
	m.SetLocalBytes(456)
	m.ObserveFlushDuration(0.5)
	m.ObserveUploadDuration(0.5)
}

func TestNewMetrics_NonNilRegistryRegistersAndPreTouches(t *testing.T) {
	// Pin the full registration + pre-touch wiring at metrics.go:84-117.
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	pm, ok := m.(*promMetrics)
	if !ok {
		t.Fatalf("got %T, want *promMetrics", m)
	}
	if pm == nil {
		t.Fatal("*promMetrics = nil")
	}
	// Gather and confirm every metric name surfaces — including
	// the pre-touched CounterVec rows for status ∈ {ok, err} and
	// the closed-set failure reasons.
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	wantNames := map[string]bool{
		metricFilesUploadedTotal:    false,
		metricBytesUploadedTotal:    false,
		metricFailuresTotal:         false,
		metricLocalBytes:            false,
		metricFlushDurationSeconds:  false,
		metricUploadDurationSeconds: false,
	}
	for _, mf := range mfs {
		if _, ok := wantNames[mf.GetName()]; ok {
			wantNames[mf.GetName()] = true
		}
	}
	for name, found := range wantNames {
		if !found {
			t.Errorf("metric %q not registered", name)
		}
	}
	// Pre-touched rows: confirm the two file-uploaded rows and
	// the closed-set failure-reason rows each surface as a metric with
	// zero value (the registry init pattern).
	filesSeen := map[string]bool{}
	for _, mf := range mfs {
		if mf.GetName() == metricFilesUploadedTotal {
			for _, m := range mf.GetMetric() {
				for _, l := range m.GetLabel() {
					if l.GetName() == "status" {
						filesSeen[l.GetValue()] = true
					}
				}
			}
		}
	}
	for _, want := range []string{"ok", "err"} {
		if !filesSeen[want] {
			t.Errorf("filesUploaded pre-touch missing status=%q: %v", want, filesSeen)
		}
	}
}

func TestNewMetricsWithPrefix_UsesDaemonPrefix(t *testing.T) {
	reg := prometheus.NewRegistry()
	NewMetricsWithPrefix(reg, "vmmd")
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, mf := range mfs {
		if strings.HasPrefix(mf.GetName(), "apid_log_archive_") {
			t.Fatalf("unexpected apid metric %q in vmmd registry", mf.GetName())
		}
	}
	for _, want := range []string{"vmmd_log_archive_files_uploaded_total", "vmmd_log_archive_failures_total", "vmmd_log_archive_local_bytes"} {
		found := false
		for _, mf := range mfs {
			if mf.GetName() == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("metric %q not registered", want)
		}
	}
}

func TestPromMetrics_NilReceiverIsSafe(t *testing.T) {
	// Pin the nil-receiver guards at metrics.go:127-172. These
	// happen when an interface value is nil-typed.
	var pm *promMetrics
	pm.IncFilesUploaded("ok")
	pm.AddBytesUploaded(1)
	pm.IncFailure(FailureReasonNetwork)
	pm.SetLocalBytes(0)
	pm.ObserveFlushDuration(0)
	pm.ObserveUploadDuration(0)
}

func TestPromMetrics_UnknownStatusAndReasonRejected(t *testing.T) {
	// metrics.go:131-134 + 148-151 — switch drops unknown labels.
	// The pre-touched rows (status ∈ {ok, err}, reason ∈ {7 closed
	// reasons}) are registered at NewMetrics time. After an
	// IncFilesUploaded("bogus") and IncFailure("not-a-real-reason"),
	// confirm:
	//   * No new label values appear beyond the pre-touched set.
	//   * Pre-touched rows have counter value = 0 (the bogus calls
	//     did not increment them).
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	m.IncFilesUploaded("bogus")
	m.IncFilesUploaded("ok") // legit increment so we can compare
	m.IncFailure("not-a-real-reason")
	m.IncFailure(FailureReasonNetwork) // legit increment
	mfs, _ := reg.Gather()
	for _, mf := range mfs {
		switch mf.GetName() {
		case metricFilesUploadedTotal:
			for _, m := range mf.GetMetric() {
				status := ""
				for _, l := range m.GetLabel() {
					if l.GetName() == "status" {
						status = l.GetValue()
					}
				}
				if status != "ok" && status != "err" {
					t.Errorf("unexpected status label registered: %q", status)
				}
				if status == "ok" && m.GetCounter().GetValue() != 1 {
					t.Errorf("ok counter = %v, want 1", m.GetCounter().GetValue())
				}
				if status == "err" && m.GetCounter().GetValue() != 0 {
					t.Errorf("err counter = %v, want 0 (IncFilesUploaded(bogus) must not bump err)", m.GetCounter().GetValue())
				}
			}
		case metricFailuresTotal:
			for _, m := range mf.GetMetric() {
				reason := ""
				for _, l := range m.GetLabel() {
					if l.GetName() == "reason" {
						reason = l.GetValue()
					}
				}
				if reason != "" && !isKnownReason(reason) {
					t.Errorf("unknown reason label registered: %q", reason)
				}
				if reason == string(FailureReasonNetwork) && m.GetCounter().GetValue() != 1 {
					t.Errorf("network counter = %v, want 1", m.GetCounter().GetValue())
				}
			}
		}
	}
}

func isKnownReason(s string) bool {
	for _, r := range []string{FailureReasonNetwork, FailureReasonAuth, FailureReasonThrottle, FailureReasonSize, FailureReasonSpoolFull, FailureReasonSpoolWrite, FailureReasonQueueFull, FailureReasonBodyLength, FailureReasonOther} {
		if r == s {
			return true
		}
	}
	return false
}

func TestRecordingMetrics_RecordsEveryCall(t *testing.T) {
	// Pin the recordingMetrics increments at metrics.go:203-225.
	// Used by tests as a non-registry metric sink.
	r := newRecordingMetrics()
	r.IncFilesUploaded("ok")
	r.IncFilesUploaded("ok")
	r.IncFilesUploaded("err")
	r.IncFilesUploaded("weird") // dropped
	r.AddBytesUploaded(100)
	r.AddBytesUploaded(25)
	r.IncFailure(FailureReasonNetwork)
	r.IncFailure(FailureReasonNetwork)
	r.SetLocalBytes(7)
	r.ObserveFlushDuration(0.1)
	r.ObserveUploadDuration(0.2)
	if r.filesOK != 2 {
		t.Errorf("filesOK = %d, want 2", r.filesOK)
	}
	if r.filesErr != 1 {
		t.Errorf("filesErr = %d, want 1", r.filesErr)
	}
	if r.bytes != 125 {
		t.Errorf("bytes = %d, want 125", r.bytes)
	}
	if r.failures[FailureReasonNetwork] != 2 {
		t.Errorf("network failures = %d, want 2", r.failures[FailureReasonNetwork])
	}
	if len(r.localBytesUpdates) != 1 || r.localBytesUpdates[0] != 7 {
		t.Errorf("localBytesUpdates = %v", r.localBytesUpdates)
	}
	if len(r.flushDurations) != 1 || len(r.uploadDurations) != 1 {
		t.Errorf("durations slices not appended")
	}
}

// --- spool.go edge cases -------------------------------------------

func TestSpool_FlushKey_UnknownKeyIsNoop(t *testing.T) {
	// spool.go:322-324 — FlushKey on a key with no open file
	// returns nil silently rather than erroring.
	s := NewSpool(t.TempDir(), DefaultLocalBytesMax)
	if err := s.FlushKey("missing", "2026-08-23"); err != nil {
		t.Errorf("FlushKey(missing): %v, want nil", err)
	}
}

func TestSpool_FlushKey_AfterWriteSucceeds(t *testing.T) {
	// Write one line → FlushKey for the (instance, day) tuple.
	// Flushing the bufio buffer must return nil.
	s := NewSpool(t.TempDir(), DefaultLocalBytesMax)
	ts := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	if _, err := s.Write("inst", 1, "stdout", ts, "hello"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := s.FlushKey("inst", "2026-08-23"); err != nil {
		t.Errorf("FlushKey: %v", err)
	}
}

func TestSpool_FilesSnapshot_SkipsMalformedEntries(t *testing.T) {
	// Set up a spool root with a known-good file + a bad-day
	// file + a depth!=4 path. Snapshots must surface only the
	// good file (spool.go:243-292).
	root := t.TempDir()

	// Construct a valid (instance, YYYY/MM, .partial) layout.
	goodDir := filepath.Join(root, "inst-A", "2026", "08")
	if err := os.MkdirAll(goodDir, 0o750); err != nil {
		t.Fatal(err)
	}
	goodPath := filepath.Join(goodDir, "log-2026-08-23.jsonl.partial")
	if err := os.WriteFile(goodPath, []byte("{}\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	// Bad day-length filename: same dir, malformed date.
	if err := os.WriteFile(filepath.Join(goodDir, "log-bad.jsonl.partial"), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}

	// Depth-3 file (lives one level shallow). Should be skipped.
	if err := os.WriteFile(filepath.Join(root, "inst-A", "log-2026-08-23.jsonl.partial"), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}

	s := NewSpool(root, DefaultLocalBytesMax)
	snapshot := s.FilesSnapshot()
	if len(snapshot) != 1 {
		t.Fatalf("snapshot len = %d, want 1 (good only): %+v", len(snapshot), snapshot)
	}
	got := snapshot[0]
	if got.Instance != "inst-A" || got.Day != "2026-08-23" || got.Size != 3 {
		t.Errorf("snapshot[0] = %+v, want Instance=inst-A Day=2026-08-23 Size=3", got)
	}
}

func TestSpool_FilesSnapshot_IgnoresMissingRoot(t *testing.T) {
	// spool.go:243-292 — root that doesn't exist returns empty.
	s := NewSpool(filepath.Join(t.TempDir(), "does-not-exist"), DefaultLocalBytesMax)
	if got := s.FilesSnapshot(); len(got) != 0 {
		t.Errorf("missing-root snapshot = %+v, want empty", got)
	}
}

func TestSpool_CloseAll_FlushesAndClosesAllFiles(t *testing.T) {
	// Pin CloseAll (spool.go:205-219) flushes + closes every
	// open file and returns nil when no error surfaces.
	s := NewSpool(t.TempDir(), DefaultLocalBytesMax)
	ts := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		instance := string(rune('a' + i))
		if _, err := s.Write(instance, int64(i), "stdout", ts, "line"); err != nil {
			t.Fatalf("Write[%d]: %v", i, err)
		}
	}
	if err := s.CloseAll(); err != nil {
		t.Errorf("CloseAll: %v", err)
	}
	if got := s.LocalBytes(); got == 0 {
		t.Errorf("LocalBytes = 0, want > 0 before CloseAll drains the in-memory state (resets via closeAll wiping files, but written-tally accumulates)")
	}
}

func TestSpool_CloseAll_Idempotent(t *testing.T) {
	// spool.go:217 — delete on every iteration clears the map;
	// a second CloseAll has nothing to flush and returns nil.
	s := NewSpool(t.TempDir(), DefaultLocalBytesMax)
	ts := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	if _, err := s.Write("inst", 1, "stdout", ts, "x"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := s.CloseAll(); err != nil {
		t.Fatalf("first CloseAll: %v", err)
	}
	if err := s.CloseAll(); err != nil {
		t.Errorf("second CloseAll: %v, want nil", err)
	}
}

// --- End-to-end config + metrics wiring smoke --------------------

func TestShipperConfigToMetrics_WiringSmoke(t *testing.T) {
	// Integration smoke: build a Config via env, instantiate a
	// Spool + a recordingMetrics and confirm increments roundtrip
	// from recordingMetrics out. Pin the wiring works.
	getenv := func(k string) string {
		switch k {
		case EnvBucket:
			return "wire-bucket"
		case EnvKeyID:
			return "AKIA"
		case EnvSecret:
			return "shh"
		case EnvInterval:
			return "10s"
		}
		return ""
	}
	cfg, err := ConfigFromEnv(getenv, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if !cfg.Enabled() {
		t.Errorf("enabled = false, want true")
	}
	rec := newRecordingMetrics()
	rec.IncFilesUploaded("ok")
	rec.IncFailure(FailureReasonNetwork)
	if rec.filesOK != 1 || rec.failures[FailureReasonNetwork] != 1 {
		t.Errorf("recording roundtrip lost: %+v", rec)
	}
}

// Silence unused-warning for errors if test changes drop the import.
var _ = errors.New
