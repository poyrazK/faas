package gateway

// Wake-ahead evidence across gateway restarts (ADR-1132, amending ADR-977).
//
// The ADR-977 learner needs ServiceWakeAheadMinWakes observed caller wakes
// before it predicts anything, and it lived only in memory, so every gateway
// restart — including each platform deploy — silenced wake-ahead until the
// evidence was relearned. The finished edge statistics are small and bounded,
// so they are written to a node-local file and restored at start. Nothing is
// written to Postgres: the gateway still never writes customer tables.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// WakeAheadSnapshotVersion is the on-disk format version. A file with any
// other version is ignored rather than misread.
const WakeAheadSnapshotVersion = 1

// WakeAheadSnapshotFile is the snapshot's name inside the state directory.
const WakeAheadSnapshotFile = "learner.json"

// DefaultWakeAheadStateDir is where gatewayd-internal keeps the snapshot
// (FAAS_GATEWAY_WAKE_AHEAD_STATE_DIR overrides it).
const DefaultWakeAheadStateDir = "/var/lib/faas/wake-ahead"

// WakeAheadSnapshot is the persisted form of the learner's finished edge
// statistics. Open follow windows and live speculations are not persisted:
// they are seconds old and meaningless after a restart.
type WakeAheadSnapshot struct {
	Version int                                `json:"version"`
	SavedAt time.Time                          `json:"saved_at"`
	Callers map[string]WakeAheadCallerSnapshot `json:"callers"`
}

// WakeAheadCallerSnapshot is one caller's learned statistics.
type WakeAheadCallerSnapshot struct {
	LastWake time.Time                        `json:"last_wake"`
	Wakes    float64                          `json:"wakes"`
	Edges    map[string]WakeAheadEdgeSnapshot `json:"edges"`
}

// WakeAheadEdgeSnapshot is one caller → target edge.
type WakeAheadEdgeSnapshot struct {
	Calls float64 `json:"calls"`
	Cold  float64 `json:"cold"`
}

// Snapshot returns the learner's finished statistics and clears the change
// flag. changed reports whether anything moved since the previous Snapshot.
func (l *WakeAheadLearner) Snapshot() (snap WakeAheadSnapshot, changed bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	changed, l.changed = l.changed, false
	snap = WakeAheadSnapshot{Version: WakeAheadSnapshotVersion, SavedAt: l.now().UTC(), Callers: map[string]WakeAheadCallerSnapshot{}}
	for app, c := range l.callers {
		if c.wakes <= 0 || len(c.edges) == 0 {
			continue
		}
		edges := make(map[string]WakeAheadEdgeSnapshot, len(c.edges))
		for target, e := range c.edges {
			edges[target] = WakeAheadEdgeSnapshot{Calls: e.calls, Cold: e.cold}
		}
		snap.Callers[app] = WakeAheadCallerSnapshot{LastWake: c.windowStart.UTC(), Wakes: c.wakes, Edges: edges}
	}
	return snap, changed
}

// Restore loads a snapshot into the learner and reports how many callers it
// kept. Callers the learner has already observed keep their live statistics.
// Invalid values are dropped, and the ADR-977 bounds (callers, edges per
// caller) apply, keeping the most recently woken callers and strongest edges.
func (l *WakeAheadLearner) Restore(snap WakeAheadSnapshot) int {
	if snap.Version != WakeAheadSnapshotVersion {
		return 0
	}
	type entry struct {
		app string
		c   WakeAheadCallerSnapshot
	}
	entries := make([]entry, 0, len(snap.Callers))
	for app, c := range snap.Callers {
		if app != "" && validSnapshotCount(c.Wakes) && c.Wakes > 0 {
			entries = append(entries, entry{app, c})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if !entries[i].c.LastWake.Equal(entries[j].c.LastWake) {
			return entries[i].c.LastWake.After(entries[j].c.LastWake)
		}
		return entries[i].app < entries[j].app
	})
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := 0
	for _, e := range entries {
		if _, live := l.callers[e.app]; live || len(l.callers) >= api.ServiceWakeAheadMaxApps {
			continue
		}
		edges := restoredEdges(e.app, e.c.Edges)
		if len(edges) == 0 {
			continue
		}
		c := &wakeAheadCaller{windowStart: e.c.LastWake, wakes: e.c.Wakes, called: map[string]bool{}, edges: edges}
		l.callers[e.app] = c
		// Republish the restored evidence (ADR-1152) so the learned edges
		// show again right after a restart, not after the caller's next wake.
		l.publishLocked(e.app, c)
		kept++
	}
	return kept
}

func restoredEdges(caller string, in map[string]WakeAheadEdgeSnapshot) map[string]*wakeAheadEdge {
	type edge struct {
		target string
		e      WakeAheadEdgeSnapshot
	}
	valid := make([]edge, 0, len(in))
	for target, e := range in {
		if target == "" || target == caller || !validSnapshotCount(e.Calls) || !validSnapshotCount(e.Cold) || e.Cold > e.Calls {
			continue
		}
		valid = append(valid, edge{target, e})
	}
	sort.Slice(valid, func(i, j int) bool {
		if valid[i].e.Calls != valid[j].e.Calls {
			return valid[i].e.Calls > valid[j].e.Calls
		}
		return valid[i].target < valid[j].target
	})
	if len(valid) > api.ServiceWakeAheadMaxEdgesPerApp {
		valid = valid[:api.ServiceWakeAheadMaxEdgesPerApp]
	}
	out := make(map[string]*wakeAheadEdge, len(valid))
	for _, v := range valid {
		out[v.target] = &wakeAheadEdge{calls: v.e.Calls, cold: v.e.Cold}
	}
	return out
}

func validSnapshotCount(v float64) bool {
	return v >= 0 && !math.IsNaN(v) && !math.IsInf(v, 0)
}

// SaveWakeAheadSnapshot writes snap to path atomically (temp file, fsync,
// rename) with owner-only permissions.
func SaveWakeAheadSnapshot(path string, snap WakeAheadSnapshot) error {
	data, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("wake-ahead snapshot: encode: %w", err)
	}
	if len(data) > api.ServiceWakeAheadSnapshotMaxBytes {
		return fmt.Errorf("wake-ahead snapshot: %d bytes exceeds the %d byte cap", len(data), api.ServiceWakeAheadSnapshotMaxBytes)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".learner-*.json")
	if err != nil {
		return fmt.Errorf("wake-ahead snapshot: create temp in %s: %w", dir, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("wake-ahead snapshot: chmod: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("wake-ahead snapshot: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("wake-ahead snapshot: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("wake-ahead snapshot: close: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("wake-ahead snapshot: rename into %s: %w", path, err)
	}
	return nil
}

// LoadWakeAheadSnapshot reads the snapshot at path. A missing file returns
// ok=false and no error; an oversized, unreadable or foreign-version file is
// an error the caller logs and ignores.
func LoadWakeAheadSnapshot(path string) (snap WakeAheadSnapshot, ok bool, err error) {
	f, err := os.Open(path) //nolint:forbidigo // operator-configured daemon state file (FAAS_GATEWAY_WAKE_AHEAD_STATE_DIR), never a customer path
	if errors.Is(err, fs.ErrNotExist) {
		return snap, false, nil
	}
	if err != nil {
		return snap, false, fmt.Errorf("wake-ahead snapshot: open: %w", err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, int64(api.ServiceWakeAheadSnapshotMaxBytes)+1))
	if err != nil {
		return snap, false, fmt.Errorf("wake-ahead snapshot: read: %w", err)
	}
	if len(data) > api.ServiceWakeAheadSnapshotMaxBytes {
		return snap, false, fmt.Errorf("wake-ahead snapshot: larger than the %d byte cap", api.ServiceWakeAheadSnapshotMaxBytes)
	}
	if err := json.Unmarshal(data, &snap); err != nil {
		return snap, false, fmt.Errorf("wake-ahead snapshot: decode: %w", err)
	}
	if snap.Version != WakeAheadSnapshotVersion {
		return snap, false, fmt.Errorf("wake-ahead snapshot: version %d, want %d", snap.Version, WakeAheadSnapshotVersion)
	}
	return snap, true, nil
}

// PersistWakeAhead restores the learner from dir at start, then writes its
// statistics there every interval when they changed and once more when ctx
// ends. It returns after the final write. Failures are logged, never fatal:
// wake-ahead without history is exactly the ADR-977 cold start.
func PersistWakeAhead(ctx context.Context, l *WakeAheadLearner, dir string, interval time.Duration, now func() time.Time, log *slog.Logger) {
	if l == nil || dir == "" {
		return
	}
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	path := filepath.Join(dir, WakeAheadSnapshotFile)
	// Production creates the directory before the unit starts (ReadWritePaths
	// requires it); single-box and test runs create it here.
	if err := os.MkdirAll(dir, 0o750); err != nil {
		log.Warn("gateway: wake-ahead snapshot directory unavailable; evidence will not persist", "dir", dir, "err", err)
		return
	}
	switch snap, ok, err := LoadWakeAheadSnapshot(path); {
	case err != nil:
		log.Warn("gateway: wake-ahead snapshot ignored", "path", path, "err", err)
	case !ok:
	case now().Sub(snap.SavedAt) > api.ServiceWakeAheadSnapshotMaxAge:
		log.Info("gateway: wake-ahead snapshot too old; starting empty", "path", path, "saved_at", snap.SavedAt)
	default:
		log.Info("gateway: wake-ahead evidence restored", "path", path, "callers", l.Restore(snap), "saved_at", snap.SavedAt)
	}
	save := func() {
		snap, changed := l.Snapshot()
		if !changed {
			return
		}
		if err := SaveWakeAheadSnapshot(path, snap); err != nil {
			log.Warn("gateway: wake-ahead snapshot write failed", "path", path, "err", err)
		}
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			save()
			return
		case <-ticker.C:
			save()
		}
	}
}
