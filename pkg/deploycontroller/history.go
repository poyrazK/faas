package deploycontroller

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// HistoryName is the activation history the controller keeps in the releases
// root. Retention, rollback and the doctor only look at release directories,
// so a file there is never mistaken for a release.
const HistoryName = ".activation-history.json"

// historyLimit bounds the history. Retention keeps only a few releases, so
// older entries could not be rolled back to anyway.
const historyLimit = 16

// history is the stack of releases the controller activated, oldest first.
// When it is in step with the current pointer its last entry is the active
// release and the entry before it is the release that was active before
// that: what a rollback restores. A deployment pushes; a rollback pops.
//
// Before it, rollback activated the newest retained directory other than the
// active one. Directory times record when a release was installed, not when it
// was active, so after deploy A, roll back, deploy B, the rollback of B
// restored A — the release the first rollback had just removed
// (cd-controlplane run 39480533579, 2026-10-20).
type history struct {
	Releases []string `json:"releases"`
}

func historyPath(releasesRoot string) string {
	return filepath.Join(releasesRoot, HistoryName)
}

// readHistory reads the history. A host whose releases were all activated
// before the history existed has none, which is an empty history.
func readHistory(releasesRoot string) (history, error) {
	body, err := os.ReadFile(historyPath(releasesRoot))
	if errors.Is(err, os.ErrNotExist) {
		return history{}, nil
	}
	if err != nil {
		return history{}, fmt.Errorf("read activation history: %w", err)
	}
	var h history
	if err := json.Unmarshal(body, &h); err != nil {
		return history{}, fmt.Errorf("decode activation history: %w", err)
	}
	for _, id := range h.Releases {
		if !plainReleaseID(id) {
			return history{}, fmt.Errorf("activation history names %q, which is not a release id", id)
		}
	}
	return h, nil
}

// predecessor returns the release that was active before active, when the
// history is in step with the current pointer and records one.
func (h history) predecessor(active string) (string, bool) {
	n := len(h.Releases)
	if n < 2 || h.Releases[n-1] != active {
		return "", false
	}
	return h.Releases[n-2], true
}

// unwindTo drops the entries above id, the release now active. A history
// that does not contain id restarts from it.
func (h history) unwindTo(id string) history {
	for i := len(h.Releases) - 1; i >= 0; i-- {
		if h.Releases[i] == id {
			return history{Releases: append([]string(nil), h.Releases[:i+1]...)}
		}
	}
	return history{Releases: []string{id}}
}

// activated records that release replaced previous.
func (h history) activated(previous, release string) history {
	next := h.unwindTo(previous)
	next.Releases = append(next.Releases, release)
	if len(next.Releases) > historyLimit {
		next.Releases = next.Releases[len(next.Releases)-historyLimit:]
	}
	return next
}

// writeHistory replaces the history atomically.
func writeHistory(releasesRoot string, h history) error {
	body, err := json.Marshal(h)
	if err != nil {
		return fmt.Errorf("encode activation history: %w", err)
	}
	tmp, err := os.CreateTemp(releasesRoot, HistoryName+".*.tmp")
	if err != nil {
		return fmt.Errorf("create activation history temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(append(body, '\n')); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write activation history: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync activation history: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close activation history: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return fmt.Errorf("chmod activation history: %w", err)
	}
	if err := os.Rename(tmpPath, historyPath(releasesRoot)); err != nil {
		return fmt.Errorf("publish activation history: %w", err)
	}
	return nil
}

// plainReleaseID reports whether id names a direct child of the releases
// root.
func plainReleaseID(id string) bool {
	return id != "" && id != "." && id != ".." && filepath.Base(id) == id
}
