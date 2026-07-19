package fcvm

import "fmt"

// Firecracker machine config + jailer invocation builders (spec §4.4, Appendix
// B). These are pure functions: given a spec they produce the exact JSON and
// argv, so the wiring is unit-testable without KVM. The metal layer marshals the
// JSON and execs the jailer.

// VMConfig is the Firecracker configuration file (`--config-file`). JSON tags
// match the Firecracker API schema exactly — do not rename.
type VMConfig struct {
	BootSource        BootSource `json:"boot-source"`
	Drives            []Drive    `json:"drives"`
	MachineConfig     Machine    `json:"machine-config"`
	NetworkInterfaces []NetIface `json:"network-interfaces"`
	// Entropy is an empty object to attach virtio-rng (always on, spec §11).
	Entropy *Entropy `json:"entropy,omitempty"`
// VsockDevice, when set, attaches a vsock device (ADR-022). The host dials
	// it from outside the chroot to trigger the post-restore resume hook
	// (guest/init/resume.go). Always attached on cold boot too, so the cold-
	// boot fallback path matches the restore path's device layout.
	//
	// JSON tag is `vsock` (NOT `vsock-device`) to match the Firecracker
	// config-file schema (FC swagger FullVmConfiguration.vsock). The wire
	// shape inside is identical to the Vsock type the PUT /vsock API
	// endpoint accepts.
	VsockDevice *VsockDevice `json:"vsock,omitempty"`
}

// VsockDevice is the Firecracker vsock binding the host uses to dial the guest
// after a restore. guest_cid must be unique per live instance on the host (we
// derive it from Lease.Slot, see GuestVsockCID). uds_path is the in-chroot
// path the jailer creates automatically; the host side of the wire reaches it
// through chrootRoot(instance).
//
// JSON tags match the Firecracker `Vsock` schema (FC swagger). `vsock_id`
// is deprecated in FC and we don't send it; the field is kept for
// documentation of why the wire tag is empty.
type VsockDevice struct {
	// ID is unused on the wire (vsock_id is deprecated in FC). Kept for
	// in-memory bookkeeping only — the JSON tag is `json:"-"` so it never
	// reaches the FC config file.
	ID string `json:"-"`
	// GuestCID is the per-instance slot-derived CID (Lease.Slot +
	// VsockCIDBase). FC requires min 3; VsockCIDBase = 0x100 satisfies.
	GuestCID uint32 `json:"guest_cid"`
	// UDSSocket is the in-chroot AF_UNIX path Firecracker listens on for
	// host-initiated connections (the host writes "CONNECT <port>\n" then
	// proxies the byte stream to the guest's AF_VSOCK listener).
	UDSSocket string `json:"uds_path"`
}

type BootSource struct {
	KernelImagePath string `json:"kernel_image_path"`
	BootArgs        string `json:"boot_args"`
}

// Drive is one virtio-blk device. The two-drive scheme (spec §4.6): drive0 is the
// shared read-only base rootfs, drive1 the per-app writable layer. Never flatten.
type Drive struct {
	DriveID      string `json:"drive_id"`
	PathOnHost   string `json:"path_on_host"`
	IsRootDevice bool   `json:"is_root_device"`
	IsReadOnly   bool   `json:"is_read_only"`
}

type Machine struct {
	VcpuCount  int  `json:"vcpu_count"`
	MemSizeMib int  `json:"mem_size_mib"`
	Smt        bool `json:"smt"`
}

type NetIface struct {
	IfaceID     string `json:"iface_id"`
	HostDevName string `json:"host_dev_name"`
}

// Entropy carries no fields; its presence enables virtio-rng.
type Entropy struct{}

// Drive ids (stable; guest-init keys overlay assembly off them).
const (
	DriveBase  = "base"
	DriveLayer = "layer"
)

// coldBootArgs is the kernel command line for a cold boot (spec §4.4: console
// off, quiet; guest-init is PID1 installed as /sbin/init). The identical inner
// world (ADR-009) is configured by the kernel's ip= autoconfig so guest-init
// carries no networking code: guest 10.0.0.2, gateway 10.0.0.1, /30 mask. Every
// VM boots with the same line — uniqueness lives entirely on the host side.
const coldBootArgs = "console=off reboot=k panic=1 pci=off quiet " +
	"ip=10.0.0.2::10.0.0.1:255.255.255.252::eth0:off init=/sbin/init"

// ColdBootSpec is everything needed to build a cold-boot VM config. RAM and vCPU
// come from the app's plan (via pkg/api limits) — never inline them here.
type ColdBootSpec struct {
	KernelPath string // /srv/fc/base/vmlinux-6.1.x
	BasePath   string // drive0 shared ro base rootfs
	LayerPath  string // drive1 per-app app layer
	VcpuCount  int    // 2, or 4 for Scale
	MemSizeMiB int    // plan RAM
	Tap        string // netns-side tap device (always "tap0")
}

// BuildColdBootConfig assembles the Firecracker config for a cold boot. MMDS and
// balloon are off in v1 (spec §4.4); virtio-rng is always attached (spec §11).
//
// slot is the per-instance slot from Lease.Slot; it must be in range [0, MaxSlots).
// It derives GuestVsockCID so the in-guest resume listener is reachable at a
// globally unique vsock address (ADR-022). The Manager passes 0 when the slot
// is not yet known (test seams); production always passes the real slot.
func BuildColdBootConfig(s ColdBootSpec, slot int) VMConfig {
	return VMConfig{
		BootSource: BootSource{KernelImagePath: s.KernelPath, BootArgs: coldBootArgs},
		Drives: []Drive{
			{DriveID: DriveBase, PathOnHost: s.BasePath, IsRootDevice: true, IsReadOnly: true},
			{DriveID: DriveLayer, PathOnHost: s.LayerPath, IsRootDevice: false, IsReadOnly: false},
		},
		MachineConfig:     Machine{VcpuCount: s.VcpuCount, MemSizeMib: s.MemSizeMiB, Smt: false},
		NetworkInterfaces: []NetIface{{IfaceID: "eth0", HostDevName: s.Tap}},
		Entropy:           &Entropy{},
		VsockDevice:       NewVsockDevice(slot),
	}
}

// NewVsockDevice builds a VsockDevice for the given slot. The UDS socket path
// is the chroot-relative name (jailer creates it automatically); the host-side
// path is chrootRoot(instance) + VsockUDSSocketName (see pkg/fcvm/vmm.go).
func NewVsockDevice(slot int) *VsockDevice {
	return &VsockDevice{
		ID:        VsockDeviceID,
		GuestCID:  GuestVsockCID(slot),
		UDSSocket: VsockUDSSocketName,
	}
}

// Validate rejects a cold-boot spec that would produce a non-bootable VM.
func (s ColdBootSpec) Validate() error {
	switch {
	case s.KernelPath == "":
		return fmt.Errorf("fcvm: cold boot: empty kernel path")
	case s.BasePath == "":
		return fmt.Errorf("fcvm: cold boot: empty base rootfs path")
	case s.LayerPath == "":
		return fmt.Errorf("fcvm: cold boot: empty app-layer path")
	case s.VcpuCount < 1:
		return fmt.Errorf("fcvm: cold boot: vcpu_count %d < 1", s.VcpuCount)
	case s.MemSizeMiB < 1:
		return fmt.Errorf("fcvm: cold boot: mem_size_mib %d < 1", s.MemSizeMiB)
	case s.Tap == "":
		return fmt.Errorf("fcvm: cold boot: empty tap device")
	}
	return nil
}

// Jailer paths (spec §8, Appendix B).
const (
	JailChrootBase = "/srv/fc/jail"
	ParentCgroup   = "faas-tenant.slice"
	FirecrackerBin = "firecracker"
	APISockName    = "api.sock"
	VMConfigName   = "vmconfig.json"
	// VsockUDSSocketName is the chroot-relative path Firecracker creates the
	// vsock UDS on when a vsock device is attached. Jailer owns it under
	// the per-instance chroot; the host side dials through chrootRoot.
	VsockUDSSocketName = "vsock.sock"
	// VsockDeviceID is the Firecracker device id used in /vsock PUT bodies
	// and referenced from the config-file.
	VsockDeviceID = "vsock-0"
)

// Vsock CID allocation (ADR-022). The Linux kernel reserves CID 0 (wildcard),
// 1 (host-hypervisor), and 2 (guest-hypervisor); Firecracker documents
// guest_cid uniqueness across simultaneously-running VMs. We use a fixed host
// CID of 3 and derive the guest CID from Lease.Slot + a base offset large
// enough to skip both the reserved range AND the common per-slot value space
// (Slot values go up to MaxSlots-1 = 9999).
//
// VsockCIDBase+slot is therefore globally unique per live instance — slot is
// already the unique-while-live root for UID/GID/HostIP (alloc.go:113).
const (
	HostVsockCID uint32 = 3
	VsockCIDBase uint32 = 0x100
)

// GuestVsockCID maps a Lease.Slot to a Firecracker guest_cid value.
func GuestVsockCID(slot int) uint32 { return VsockCIDBase + uint32(slot) }

// JailerSpec is the input to the jailer invocation for one instance.
type JailerSpec struct {
	Instance string // jailer --id and chroot leaf
	UID      int    // from the Lease
	GID      int    // from the Lease
	Netns    string // netns name, e.g. fc-<instance>
	ExecFile string // path to the firecracker binary jailer copies into the chroot
}

// JailerCommand builds the jailer argv (Appendix B). vmmd execs this as root; the
// jailer drops privileges to UID/GID, chroots, applies seccomp, and joins the
// cgroup scope before executing firecracker.
//
// jailer requires --exec-file (the firecracker binary it copies into the chroot,
// whose basename also names the chroot dir — see JailChrootBase/FirecrackerBin).
// Everything after `--` is firecracker's OWN argv (no binary name — jailer runs
// the exec-file): only --api-sock here, so the control socket always exists; the
// caller appends --config-file for a cold boot (Restore drives the API instead).
//
// --cgroup cpu.weight=256 is mandatory on the v2 path: without at least one
// --cgroup-param, jailer (FC v1.7+) only attaches the jailer PID to the parent
// slice and never creates a per-instance child scope. The vmm wrapper writes
// memory.max into `<cgroupRoot>/faas-tenant.slice/vm-<instance>.scope` after
// bringUp; the scope must exist by then or writeMemoryMax returns IsNotExist.
// cpu.weight=256 is a neutral default (kernel normalises 100-1000, 256 ~ mid).
func JailerCommand(s JailerSpec) []string {
	execFile := s.ExecFile
	if execFile == "" {
		execFile = FirecrackerBin
	}
	return []string{
		"jailer",
		"--id", s.Instance,
		"--uid", fmt.Sprintf("%d", s.UID),
		"--gid", fmt.Sprintf("%d", s.GID),
		"--exec-file", execFile,
		"--chroot-base-dir", JailChrootBase,
		"--netns", "/run/netns/" + s.Netns,
		"--cgroup-version", "2",
		"--parent-cgroup", ParentCgroup,
		"--cgroup", "cpu.weight=256",
		"--",
		"--api-sock", APISockName,
	}
}
