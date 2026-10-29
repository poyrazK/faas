package releasehistory

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/releasehistory/sqlc"
)

// A compute host has no owner access to the history, so its deployctl spools
// each finished activation to a local file instead, and CD imports the file on
// the control plane (`deployctl record`). Each line carries the id it got on
// the host, so importing a spool again adds nothing.

// SpoolKeep is how many activations a host's spool keeps, newest last. CD
// imports it after every deploy, so a few hundred covers any gap.
const SpoolKeep = 256

// Line is one spooled activation, finished.
type Line struct {
	ID                string    `json:"id"`
	Host              string    `json:"host"`
	Kind              string    `json:"kind"`
	ReleaseID         string    `json:"release_id"`
	CommitSHA         string    `json:"commit_sha,omitempty"`
	PreviousReleaseID string    `json:"previous_release_id,omitempty"`
	Services          []string  `json:"services,omitempty"`
	Actor             string    `json:"actor"`
	Status            string    `json:"status"`
	Detail            string    `json:"detail,omitempty"`
	StartedAt         time.Time `json:"started_at"`
	FinishedAt        time.Time `json:"finished_at"`
}

// AppendSpool appends l to the spool at path, keeping its newest SpoolKeep
// lines. The caller holds the host's deploy lock, so no other writer runs.
func AppendSpool(path string, l Line) error {
	l.Detail = truncate(l.Detail, MaxDetail)
	l.Actor = truncate(l.Actor, MaxActor)
	encoded, err := json.Marshal(l)
	if err != nil {
		return fmt.Errorf("releasehistory: encode spool line: %w", err)
	}
	var lines [][]byte
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("releasehistory: read spool: %w", err)
	}
	for _, line := range bytes.Split(existing, []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			lines = append(lines, line)
		}
	}
	lines = append(lines, encoded)
	if len(lines) > SpoolKeep {
		lines = lines[len(lines)-SpoolKeep:]
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("releasehistory: spool directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".activations-*.tmp")
	if err != nil {
		return fmt.Errorf("releasehistory: spool temp: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(bytes.Join(lines, []byte("\n")), '\n')); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("releasehistory: write spool: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("releasehistory: write spool: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("releasehistory: publish spool: %w", err)
	}
	return nil
}

// ReadSpool decodes a spool. A line that does not decode stops the read: the
// spool is written whole by AppendSpool, so it means the input is not one.
func ReadSpool(r io.Reader) ([]Line, error) {
	var lines []Line
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for n := 1; scanner.Scan(); n++ {
		raw := bytes.TrimSpace(scanner.Bytes())
		if len(raw) == 0 {
			continue
		}
		var l Line
		if err := json.Unmarshal(raw, &l); err != nil {
			return nil, fmt.Errorf("releasehistory: spool line %d: %w", n, err)
		}
		lines = append(lines, l)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("releasehistory: read spool: %w", err)
	}
	return lines, nil
}

// Import records a spooled activation under its own id and reports whether
// it was new. The caller sets Host: a spool never names its own host.
func (s *Store) Import(ctx context.Context, l Line) (bool, error) {
	if !finalStatus(l.Status) {
		return false, fmt.Errorf("releasehistory: spooled activation %s has status %q, not a final one", l.ID, l.Status)
	}
	if l.Kind != KindDeploy && l.Kind != KindRollback {
		return false, fmt.Errorf("releasehistory: spooled activation %s has kind %q", l.ID, l.Kind)
	}
	var id pgtype.UUID
	if err := id.Scan(l.ID); err != nil {
		return false, fmt.Errorf("releasehistory: spooled activation id %q: %w", l.ID, err)
	}
	n, err := s.q.ImportActivation(ctx, s.db, sqlc.ImportActivationParams{
		ID:                id,
		Host:              l.Host,
		Kind:              l.Kind,
		ReleaseID:         l.ReleaseID,
		CommitSha:         optionalText(l.CommitSHA),
		PreviousReleaseID: optionalText(l.PreviousReleaseID),
		Services:          l.Services,
		Actor:             truncate(l.Actor, MaxActor),
		Status:            l.Status,
		Detail:            truncate(l.Detail, MaxDetail),
		StartedAt:         timestamptz(l.StartedAt),
		FinishedAt:        timestamptz(l.FinishedAt),
	})
	if err != nil {
		return false, fmt.Errorf("releasehistory: import activation %s: %w", l.ID, err)
	}
	return n == 1, nil
}
