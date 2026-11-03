package dbarchive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Holds (ADR-1027 environment clones). A hold pins an instant in a
// database's archive: pruning keeps what recovering to it needs, however
// old it gets, until the hold is removed. A native snapshot is a hold.
// With a bucket, holds live in it only, so every pruner of the database
// sees them: its node, the next one after a move, and any node pruning a
// lost node's archive. Without one they live beside the node's archive.
// Dropping the database's archive drops its holds.

// Hold is one pin.
type Hold struct {
	Name        string    `json:"name"`
	PointInTime time.Time `json:"point_in_time"`
	CreatedAt   time.Time `json:"created_at"`
}

var holdName = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ValidHoldName reports whether name can name a hold.
func ValidHoldName(name string) bool { return holdName.MatchString(name) }

// HoldKey is the remote key of a hold.
func HoldKey(prefix, databaseID, name string) string {
	return strings.TrimSuffix(prefix, "/") + "/" + databaseID + "/holds/" + name
}

func (s *TieredStore) holdsDir(databaseID string) string {
	return filepath.Join(s.dir, databaseID, "holds")
}

func validHoldRef(databaseID, name string) error {
	if !ValidDatabaseID(databaseID) || !ValidHoldName(name) {
		return fmt.Errorf("dbarchive: invalid hold %q/%q", databaseID, name)
	}
	return nil
}

// PutHold pins point under name. An existing hold of that name and point
// is returned as it is, its creation time unchanged; another point is
// ErrConflict.
func (s *TieredStore) PutHold(ctx context.Context, databaseID, name string, point time.Time) (Hold, error) {
	if err := validHoldRef(databaseID, name); err != nil {
		return Hold{}, err
	}
	if point.IsZero() {
		return Hold{}, errors.New("dbarchive: a hold needs a point in time")
	}
	point = point.UTC().Truncate(time.Microsecond)
	// One writer at a time, so two placements of one name agree on its
	// creation time.
	s.holdsMu.Lock()
	defer s.holdsMu.Unlock()
	existing, err := s.GetHold(ctx, databaseID, name)
	if err == nil {
		if !existing.PointInTime.Equal(point) {
			return Hold{}, ErrConflict
		}
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Hold{}, err
	}
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	// Never before its point, whichever clock chose the point.
	hold := Hold{Name: name, PointInTime: point, CreatedAt: now().UTC().Truncate(time.Microsecond)}
	if hold.CreatedAt.Before(point) {
		hold.CreatedAt = point
	}
	body, err := json.Marshal(hold)
	if err != nil {
		return Hold{}, err
	}
	if s.Remote != nil {
		// The mirror copies the hold from the bucket once it is there.
		if err := s.journalOffsite(offsiteOp{Op: offsiteCopy, Database: databaseID, Path: "holds/" + name}); err != nil {
			return Hold{}, err
		}
		return hold, s.Remote.Put(ctx, HoldKey(s.Prefix, databaseID, name), bytes.NewReader(body), int64(len(body)))
	}
	if err := os.MkdirAll(s.holdsDir(databaseID), 0o700); err != nil {
		return Hold{}, err
	}
	return hold, writeFileSync(filepath.Join(s.holdsDir(databaseID), name), body)
}

// GetHold returns a hold, or ErrNotFound.
func (s *TieredStore) GetHold(ctx context.Context, databaseID, name string) (Hold, error) {
	if err := validHoldRef(databaseID, name); err != nil {
		return Hold{}, err
	}
	var body []byte
	if s.Remote != nil {
		var buf bytes.Buffer
		if err := s.Remote.Get(ctx, HoldKey(s.Prefix, databaseID, name), &buf); err != nil {
			return Hold{}, err
		}
		body = buf.Bytes()
	} else {
		raw, err := os.ReadFile(filepath.Join(s.holdsDir(databaseID), name)) //nolint:gosec // validated name under the store root
		if errors.Is(err, os.ErrNotExist) {
			return Hold{}, ErrNotFound
		}
		if err != nil {
			return Hold{}, err
		}
		body = raw
	}
	var hold Hold
	if err := json.Unmarshal(body, &hold); err != nil || hold.Name != name || hold.PointInTime.IsZero() || hold.CreatedAt.IsZero() {
		return Hold{}, fmt.Errorf("dbarchive: hold %s/%s is corrupt", databaseID, name)
	}
	return hold, nil
}

// DeleteHold removes a hold; a missing one is success.
func (s *TieredStore) DeleteHold(ctx context.Context, databaseID, name string) error {
	if err := validHoldRef(databaseID, name); err != nil {
		return err
	}
	s.holdsMu.Lock()
	defer s.holdsMu.Unlock()
	if s.Remote != nil {
		if err := s.journalOffsite(offsiteOp{Op: offsiteDelete, Database: databaseID, Path: "holds/" + name}); err != nil {
			return err
		}
		return s.Remote.Delete(ctx, HoldKey(s.Prefix, databaseID, name))
	}
	if err := os.Remove(filepath.Join(s.holdsDir(databaseID), name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// holdFloor is the earliest point any of the database's holds pins; ok is
// false when it has none. A hold that cannot be read fails it, so pruning
// never runs past a pin it could not see.
func (s *TieredStore) holdFloor(ctx context.Context, databaseID string) (floor time.Time, ok bool, err error) {
	var names []string
	if s.Remote != nil {
		prefix := HoldKey(s.Prefix, databaseID, "")
		keys, err := s.Remote.List(ctx, prefix)
		if err != nil {
			return time.Time{}, false, err
		}
		for _, k := range keys {
			names = append(names, strings.TrimPrefix(k, prefix))
		}
	} else {
		entries, err := os.ReadDir(s.holdsDir(databaseID))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return time.Time{}, false, err
		}
		for _, e := range entries {
			names = append(names, e.Name())
		}
	}
	for _, name := range names {
		if !ValidHoldName(name) {
			continue // a write in flight, or not a hold
		}
		hold, err := s.GetHold(ctx, databaseID, name)
		if errors.Is(err, ErrNotFound) {
			continue // removed meanwhile
		}
		if err != nil {
			return time.Time{}, false, err
		}
		if !ok || hold.PointInTime.Before(floor) {
			floor, ok = hold.PointInTime, true
		}
	}
	return floor, ok, nil
}
