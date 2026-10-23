package fcvm

// The journal's copy of the node's vmmd ownership epoch (vmmd restart
// adoption ADR §3). It lives in the resource journal directory as ".epoch",
// under the same private-file, fsync and exclusive-lock rules, so a
// successor can tell whether the database epoch is explained by this
// node's own last claim. It is written only by an adopting vmmd; every
// release since slice 1's reader tolerates the file.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"

	"github.com/google/uuid"
)

const resourceEpochFile = ".epoch"

// Epoch claim states: written before and after the database claim.
const (
	JournalEpochClaiming = "claiming"
	JournalEpochClaimed  = "claimed"
)

// JournalEpoch is one claim of the node's epoch.
type JournalEpoch struct {
	NodeID      string `json:"node_id"`
	Epoch       uint64 `json:"epoch"`
	BootID      string `json:"boot_id"`
	Incarnation string `json:"incarnation"`
	State       string `json:"state"`
}

func (e JournalEpoch) validate() error {
	for _, id := range []string{e.NodeID, e.BootID, e.Incarnation} {
		if u, err := uuid.Parse(id); err != nil || u.String() != id || u == uuid.Nil {
			return errors.New("resource journal: invalid epoch identity")
		}
	}
	if e.Epoch == 0 || e.Epoch >= 1<<62 || (e.State != JournalEpochClaiming && e.State != JournalEpochClaimed) {
		return errors.New("resource journal: invalid epoch claim")
	}
	return nil
}

// ReadEpoch returns the journal's last epoch claim; ok is false when this
// journal never held one.
func (j *ResourceJournal) ReadEpoch() (JournalEpoch, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return JournalEpoch{}, false, errResourceJournalClosed
	}
	f, err := j.dir.OpenFile(resourceEpochFile, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return JournalEpoch{}, false, nil
	}
	if err != nil {
		return JournalEpoch{}, false, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return JournalEpoch{}, false, err
	}
	if err := journalPrivateFile(info); err != nil {
		return JournalEpoch{}, false, err
	}
	var e JournalEpoch
	d := json.NewDecoder(io.LimitReader(f, 4096))
	d.DisallowUnknownFields()
	if err := d.Decode(&e); err != nil {
		return JournalEpoch{}, false, fmt.Errorf("resource journal: epoch: %w", err)
	}
	if err := e.validate(); err != nil {
		return JournalEpoch{}, false, err
	}
	return e, true, nil
}

// WriteEpoch durably replaces the journal's epoch claim.
func (j *ResourceJournal) WriteEpoch(e JournalEpoch) error {
	if err := e.validate(); err != nil {
		return err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return errResourceJournalClosed
	}
	return j.persistFile(resourceEpochFile, b, "", nil)
}
