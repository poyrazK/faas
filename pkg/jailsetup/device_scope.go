package jailsetup

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/google/uuid"
)

type DeviceFDIdentity struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

func (i *DeviceFDIdentity) UnmarshalJSON(data []byte) error {
	if _, err := deviceObjectFields(data, []string{"device", "inode"}); err != nil {
		return err
	}
	type plain DeviceFDIdentity
	return json.Unmarshal(data, (*plain)(i))
}

// Child descriptors are fixed: gate=3, original root=4, mount namespace=5,
// pidfd=6, original TUN=7. ParentFDs record only the producer's own copies.
type DeviceSetupScope struct {
	// Version 2 is KVM-only: descriptor 7 repeats the pinned root, never TUN.
	Version         int              `json:"version,omitempty"`
	Generation      string           `json:"generation"`
	BootID          string           `json:"boot_id"`
	PID             int              `json:"pid"`
	StartTime       uint64           `json:"start_time"`
	UID             int              `json:"uid"`
	GID             int              `json:"gid"`
	ParentPID       int              `json:"parent_pid"`
	ParentStartTime uint64           `json:"parent_start_time"`
	ParentFDs       [4]int           `json:"parent_fds"`
	Root            DeviceFDIdentity `json:"root"`
	Namespace       DeviceFDIdentity `json:"namespace"`
	PIDHandle       DeviceFDIdentity `json:"pid_handle"`
	Tun             DeviceFDIdentity `json:"tun"`
	RootMountID     uint64           `json:"root_mount_id"`
	TunMountID      uint64           `json:"tun_mount_id"`
	TunMode         uint32           `json:"tun_mode"`
}

func (s *DeviceSetupScope) UnmarshalJSON(data []byte) error {
	names := []string{"generation", "boot_id", "pid", "start_time", "uid", "gid", "parent_pid", "parent_start_time", "parent_fds", "root", "namespace", "pid_handle", "tun", "root_mount_id", "tun_mount_id", "tun_mode"}
	fields, err := deviceObjectFields(data, names)
	if err != nil {
		fields, err = deviceObjectFields(data, append(names, "version"))
	}
	if err != nil {
		return err
	}
	var descriptors []json.RawMessage
	if err := json.Unmarshal(fields["parent_fds"], &descriptors); err != nil || len(descriptors) != 4 {
		return errors.New("native jail device scope: exactly four parent descriptors required")
	}
	type plain DeviceSetupScope
	return json.Unmarshal(data, (*plain)(s))
}

func (s DeviceSetupScope) Validate() error {
	for _, value := range []string{s.Generation, s.BootID} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || id.String() != value {
			return errors.New("native jail device scope: invalid generation or kernel boot")
		}
	}
	if s.PID <= 0 || s.StartTime == 0 || s.ParentPID <= 0 || s.ParentStartTime == 0 || s.UID < 20000 || s.UID > 29999 || s.GID != s.UID || s.RootMountID == 0 || s.TunMountID == 0 {
		return errors.New("native jail device scope: incomplete original task, mounts or lease")
	}
	if s.Version == 2 {
		if s.Tun != s.Root || s.TunMountID != s.RootMountID || s.TunMode != 0 {
			return errors.New("native jail device scope: KVM-only authority includes TUN")
		}
	} else if s.Version != 0 || s.TunMode&^0o777 != 0 || s.TunMode&0o006 != 0o006 {
		return errors.New("native jail device scope: invalid device version or TUN mode")
	}
	seen := make(map[int]bool)
	for _, fd := range s.ParentFDs {
		if fd < 3 || seen[fd] {
			return errors.New("native jail device scope: invalid parent descriptors")
		}
		seen[fd] = true
	}
	for _, identity := range s.Identities() {
		if identity.Inode == 0 {
			return errors.New("native jail device scope: missing input identity")
		}
	}
	return nil
}

func (s DeviceSetupScope) Identities() [4]DeviceFDIdentity {
	return [4]DeviceFDIdentity{s.Root, s.Namespace, s.PIDHandle, s.Tun}
}

type DeviceSetupReceipt struct {
	Scope         DeviceSetupScope `json:"scope"`
	DevMountID    uint64           `json:"dev_mount_id"`
	TunMountID    uint64           `json:"tun_mount_id"`
	KVM           DeviceFDIdentity `json:"kvm"`
	KVMAPI        int              `json:"kvm_api"`
	TunAccessible bool             `json:"tun_accessible"`
}

func (r *DeviceSetupReceipt) UnmarshalJSON(data []byte) error {
	if _, err := deviceObjectFields(data, []string{"scope", "dev_mount_id", "tun_mount_id", "kvm", "kvm_api", "tun_accessible"}); err != nil {
		return err
	}
	type plain DeviceSetupReceipt
	return json.Unmarshal(data, (*plain)(r))
}
func (r DeviceSetupReceipt) Validate(expected DeviceSetupScope) error {
	if err := r.Scope.Validate(); err != nil {
		return err
	}
	if r.Scope != expected || r.DevMountID == 0 || r.KVM.Inode == 0 || r.KVMAPI != 12 {
		return errors.New("native jail device receipt: original scope or device-access proof is incomplete")
	}
	if expected.Version == 2 {
		if r.TunMountID != 0 || r.TunAccessible {
			return errors.New("native jail device receipt: KVM-only jail gained TUN")
		}
	} else if r.TunMountID == 0 || r.DevMountID == r.TunMountID || !r.TunAccessible {
		return errors.New("native jail device receipt: TUN access proof missing")
	}
	return nil
}

func deviceObjectFields(data []byte, required []string) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	start, err := d.Token()
	if err != nil || start != json.Delim('{') {
		return nil, errors.New("native jail device scope: expected object")
	}
	allowed := make(map[string]bool)
	for _, name := range required {
		allowed[name] = true
	}
	fields := make(map[string]json.RawMessage)
	for d.More() {
		key, err := d.Token()
		name, ok := key.(string)
		if err != nil || !ok || !allowed[name] || fields[name] != nil {
			return nil, errors.New("native jail device scope: unknown or duplicate field")
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return nil, err
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, errors.New("native jail device scope: null field")
		}
		fields[name] = value
	}
	if _, err := d.Token(); err != nil {
		return nil, err
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) || len(fields) != len(required) {
		return nil, errors.New("native jail device scope: missing or trailing data")
	}
	return fields, nil
}
