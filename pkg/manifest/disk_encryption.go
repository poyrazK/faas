package manifest

import (
	"fmt"
	"regexp"
)

// DiskEncryption encrypts each host's data volume at rest
// (ADR-20261109120000). The volume is LUKS2 and unlocks at boot through
// Tang key servers on the other hosts of the fleet, over the private network:
// a disk read outside the fleet (a provider rescue boot, a replaced drive)
// cannot be opened. Absent leaves the volume plain.
type DiskEncryption struct {
	// TangPort is the port every host's Tang server listens on, private
	// network only. Zero means 7500.
	TangPort int `yaml:"tang_port,omitempty"`
	// RecoveryRecipients are operator age public keys (age1…). Each volume's
	// recovery key is sealed to them and kept beside the volume, so the
	// volume still opens if every Tang server is lost. Keep the identities
	// off the fleet.
	RecoveryRecipients []string `yaml:"recovery_recipients"`
}

// DefaultTangPort is the Tang port when the manifest names none.
const DefaultTangPort = 7500

var ageRecipientRe = regexp.MustCompile(`^age1[02-9ac-hj-np-z]{58}$`)

// Port returns the Tang port, defaulted.
func (d *DiskEncryption) Port() int {
	if d == nil || d.TangPort == 0 {
		return DefaultTangPort
	}
	return d.TangPort
}

func (m *Manifest) validateDiskEncryption() Errors {
	d := m.DiskEncryption
	if d == nil {
		return nil
	}
	var errs Errors
	if d.TangPort != 0 && (d.TangPort < 1024 || d.TangPort > 65535) {
		errs = append(errs, Error{"disk_encryption.tang_port", fmt.Sprintf("%d is outside 1024-65535", d.TangPort)})
	}
	if len(d.RecoveryRecipients) == 0 {
		errs = append(errs, Error{"disk_encryption.recovery_recipients",
			"name at least one operator age recipient; without one, losing every Tang server loses the data"})
	}
	for i, r := range d.RecoveryRecipients {
		if !ageRecipientRe.MatchString(r) {
			errs = append(errs, Error{fmt.Sprintf("disk_encryption.recovery_recipients[%d]", i),
				"must be an age public key (age1…), never an identity"})
		}
	}
	// A host never unlocks from its own Tang server: its keys sit on the
	// plain root disk beside the volume they would open. Control-plane and
	// compute hosts serve Tang; database hosts and the standby only unlock.
	if len(TangServers(m.Fleet.Hosts)) < 2 {
		errs = append(errs, Error{"disk_encryption",
			"needs at least two control-plane or compute hosts: each unlocks from the other hosts' Tang servers"})
	}
	// Tang is reached over the private network before anything else runs,
	// at boot and on a fresh host (the storage role runs before the overlay
	// role). Only a provider network that is up by itself (static: an OVH
	// vRack, a cloud VPC) gives that.
	if m.Overlay.Provider != "static" {
		errs = append(errs, Error{"disk_encryption",
			fmt.Sprintf("needs overlay.provider static (a provider private network up at boot), not %q: volumes unlock over it before the overlay starts", m.Overlay.Provider)})
	}
	return errs
}

// TangServers are the hosts that run a Tang key server: every fleet host
// except database hosts, which only unlock from them.
func TangServers(hosts []Host) []Host {
	var out []Host
	for _, h := range hosts {
		if h.Role != RoleDatabase {
			out = append(out, h)
		}
	}
	return out
}
