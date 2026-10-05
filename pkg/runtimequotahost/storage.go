package runtimequotahost

// adr: 619
import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

// Open requires private persistent storage owned by this daemon's UID. BootID
// must come from the kernel, never from a caller or scheduler. Epochs remain
// stable across daemon restarts; a kernel reboot creates a new immutable epoch.
func Open(dir, nodeID, bootID string) (_ *Journal, err error) {
	if !filepath.IsAbs(dir) || !validID(nodeID) || !validID(bootID) {
		return nil, ErrBinding
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("host journal directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || !privateFile(info) {
		return nil, errors.Join(ErrStorage, err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	j := &Journal{root: root, epochs: make(map[string]Identity), permits: make(map[string]PermitRecord), seats: make(map[string]SeatRecord), attempts: make(map[string]string), parentPins: make(map[string]runtimequota.CapacityPermit), permitSeats: make(map[string]map[string]bool), active: make(map[string]int), seatOps: make(map[string]int), resuming: make(map[string]bool), changed: make(chan struct{})}
	defer func() {
		if err != nil {
			if j.lock != nil {
				_ = j.lock.Close()
			}
			_ = root.Close()
		}
	}()
	j.lock, err = root.OpenFile(".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	info, err = j.lock.Stat()
	if err != nil || !info.Mode().IsRegular() || !privateFile(info) {
		return nil, errors.Join(ErrStorage, err)
	}
	if err = syscall.Flock(int(j.lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, errors.Join(ErrBusy, err)
	}
	if err = j.load(nodeID, bootID); err != nil {
		return nil, errors.Join(ErrStorage, err)
	}
	// Persist the journal directory entry too, including a newly created one.
	parent, err := os.OpenFile(filepath.Dir(dir), os.O_RDONLY|syscall.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	err = errors.Join(parent.Sync(), parent.Close())
	if err != nil {
		return nil, err
	}
	return j, nil
}

func privateFile(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid()) && info.Mode().Perm()&0077 == 0 && (info.IsDir() || stat.Nlink == 1)
}

func (j *Journal) read(name string, value any) error {
	f, err := j.root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !privateFile(info) || info.Size() > api.RegionalRuntimeHostRecordMaxBytes {
		return errors.Join(ErrStorage, err)
	}
	raw, err := io.ReadAll(io.LimitReader(f, api.RegionalRuntimeHostRecordMaxBytes+1))
	if err != nil || len(raw) > api.RegionalRuntimeHostRecordMaxBytes {
		return errors.Join(ErrStorage, err)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return ErrStorage
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(raw, canonical) {
		return errors.Join(ErrStorage, err)
	}
	return nil
}

func (j *Journal) write(name string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > api.RegionalRuntimeHostRecordMaxBytes {
		return j.fail(errors.Join(ErrStorage, err))
	}
	tmp := ".tmp-" + uuid.NewString()
	f, err := j.root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return j.fail(err)
	}
	_, writeErr := f.Write(raw)
	err = errors.Join(writeErr, f.Sync(), f.Close())
	if err != nil {
		return j.fail(err)
	}
	if err = j.root.Rename(tmp, name); err != nil {
		return j.fail(err)
	}
	if j.beforeSync != nil {
		if err = j.beforeSync(); err != nil {
			return j.fail(err)
		}
	}
	dir, err := j.root.OpenFile(".", os.O_RDONLY|syscall.O_DIRECTORY, 0)
	if err != nil {
		return j.fail(err)
	}
	if err = errors.Join(dir.Sync(), dir.Close()); err != nil {
		return j.fail(err)
	}
	return nil
}

func (j *Journal) load(nodeID, bootID string) error {
	dir, err := j.root.OpenFile(".", os.O_RDONLY|syscall.O_DIRECTORY, 0)
	if err != nil {
		return err
	}
	files, readErr := dir.ReadDir(-1)
	if err = errors.Join(readErr, dir.Close()); err != nil {
		return err
	}
	hasIdentity := false
	for _, f := range files {
		name := f.Name()
		switch {
		case name == ".lock":
		case name == "identity.json":
			if err = j.read(name, &j.identity); err != nil {
				return err
			}
			hasIdentity = true
		case strings.HasPrefix(name, ".tmp-") && validID(strings.TrimPrefix(name, ".tmp-")):
			// Incomplete atomic writes are retained for diagnosis. Only the
			// renamed, fsynced canonical object can be execution bookkeeping.
		case strings.HasPrefix(name, "epoch-"):
			var i Identity
			if err = j.read(name, &i); err != nil || !validIdentity(i) || name != "epoch-"+i.EpochID+".json" || i.NodeID != nodeID {
				return errors.Join(ErrBinding, err)
			}
			j.epochs[i.EpochID] = i
		case strings.HasPrefix(name, "permit-"):
			var r PermitRecord
			if err = j.read(name, &r); err != nil || r.Version != 1 || r.Permit.Validate() != nil || name != "permit-"+r.Permit.ID+".json" || (!r.Registered && r.FenceID == "") || (r.FenceID != "" && !validID(r.FenceID)) {
				return errors.Join(ErrBinding, err)
			}
			j.permits[r.Permit.ID] = r
		case strings.HasPrefix(name, "seat-"):
			var r SeatRecord
			if err = j.read(name, &r); err != nil || r.Version != 1 || r.Seat.Validate() != nil || name != "seat-"+r.Seat.ID+".json" || (!r.Registered && r.FenceID == "") || (r.FenceID != "" && !validID(r.FenceID)) {
				return errors.Join(ErrBinding, err)
			}
			j.seats[r.Seat.ID] = r
		default:
			return fmt.Errorf("host journal unexpected file %q: %w", name, ErrStorage)
		}
	}
	if !hasIdentity {
		if len(files) != 1 { // only our lock is permitted before initialization
			return ErrStorage
		}
		j.identity = Identity{Version: 1, NodeID: nodeID, EpochID: uuid.NewString(), BootID: bootID}
		if err = j.write("identity.json", j.identity); err != nil {
			return err
		}
	} else if !validIdentity(j.identity) || j.identity.NodeID != nodeID {
		return ErrBinding
	}
	if old, ok := j.epochs[j.identity.EpochID]; ok && old != j.identity {
		return ErrBinding
	}
	if _, ok := j.epochs[j.identity.EpochID]; !ok {
		if err = j.write("epoch-"+j.identity.EpochID+".json", j.identity); err != nil {
			return err
		}
		j.epochs[j.identity.EpochID] = j.identity
	}
	if j.identity.BootID != bootID {
		// A previously used boot ID must not reactivate an older epoch.
		for _, old := range j.epochs {
			if old.BootID == bootID {
				return ErrBinding
			}
		}
		j.identity = Identity{Version: 1, NodeID: nodeID, EpochID: uuid.NewString(), BootID: bootID}
		// identity first: restart can recover its epoch copy after a crash.
		if err = j.write("identity.json", j.identity); err != nil {
			return err
		}
		if err = j.write("epoch-"+j.identity.EpochID+".json", j.identity); err != nil {
			return err
		}
		j.epochs[j.identity.EpochID] = j.identity
	}
	for _, r := range j.permits {
		if err = j.indexPermit(r.Permit); err != nil {
			return err
		}
	}
	openSeats := make(map[string]string)
	for _, r := range j.seats {
		if err = j.indexPermit(r.Seat.Permit); err != nil {
			return err
		}
		if r.Registered {
			parent, ok := j.permits[r.Seat.Permit.ID]
			if !ok || !parent.Registered || parent.Permit != r.Seat.Permit {
				return ErrBinding
			}
		}
		if r.Registered && r.FenceID == "" {
			if old := openSeats[r.Seat.Permit.ID]; old != "" && old != r.Seat.ID {
				return ErrBinding
			}
			openSeats[r.Seat.Permit.ID] = r.Seat.ID
		}
		j.indexSeat(r)
	}
	return nil
}

func validIdentity(i Identity) bool {
	return i.Version == 1 && validID(i.NodeID) && validID(i.EpochID) && validID(i.BootID)
}

func (j *Journal) indexPermit(p runtimequota.CapacityPermit) error {
	if err := j.checkPermit(p, false); err != nil {
		return err
	}
	j.attempts[attemptKey(p)] = p.ID
	j.parentPins[p.ID] = p
	return nil
}
