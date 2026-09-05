package fcvm

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

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
	// EphemeralWritable is true only for builder VMs. Their drive1 is a
	// unique scratch image deleted immediately after export, so provisioning
	// may hard-link it instead of copying multi-gigabyte bytes. App VMs keep
	// the default false and retain copy-on-write isolation.
	EphemeralWritable bool `json:"-"`
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
//
// DriveBase and DriveLayer are the legacy single-workload drive
// ids. DriveLayerMain + DriveSidecarPrefix are the PR-B additions
// (issue #463 / ADR-069). DriveLayer remains as an alias of
// DriveLayerMain so the legacy single-workload path keeps
// working — guest-init keys off DriveLayerMain for new builds and
// the overlayfs lowerdir stack is ordered base → main →
// sidecar-1 → … → sidecar-N.
const (
	DriveBase          = "base"
	DriveLayer         = "layer"      // legacy alias for DriveLayerMain
	DriveLayerMain     = "layer-main" // PR-B canonical main workload drive id
	DriveSidecarPrefix = "layer-sidecar-"
	// WorkloadNameMain is the reserved name on WorkloadSpec.Name / .Type
	// for the main workload. Sidecar names must not collide with it
	// (buildWorkloadsForColdBoot rejects "main" with a host-side
	// defence-in-depth; the apid gate is the user-facing surface).
	WorkloadNameMain = "main"
)

// Job-task vsock surface (issue #1184 Workstream A / ADR-099).
//
// VsockJobExitPort is the AF_VSOCK port the guest-init job
// supervisor (guest/init/job_supervisor_linux.go, M8) writes the
// terminal exit envelope to via DGRAM. The port NUMBER matches
// VsockCharacterizationHostPort = 1026 (the wake-time characterize
// channel); the discriminator is the socket TYPE:
//   - characterize: STREAM, host-initiated (gatewayd-internal opens
//     the connection, guest-init accepts).
//   - job_exit:     DGRAM,   guest-initiated (guest-init writes the
//     envelope, vmmd reads it via the per-VM vsock
//     device).
//
// VsockJobExitMsgType is the vsock message type byte the host
// expects in the first byte of every job-exit DGRAM. Any other
// value triggers a parse error and the DGRAM is dropped (vmmd logs
// at WARN).
const (
	VsockJobExitPort    = 1026
	VsockJobExitMsgType = 4
)

// coldBootArgs is the kernel command line for a cold boot. Firecracker captures
// the serial stream in the per-instance console log, so keep the guest console
// enabled: guest-init reports early mount/pivot failures there before vmmd can
// observe a readiness failure. The identical inner
// world (ADR-009) is configured by the kernel's ip= autoconfig so guest-init
// carries no networking code: guest 10.0.0.2, gateway 10.0.0.1, /30 mask. Every
// VM boots with the same line — uniqueness lives entirely on the host side.
const coldBootArgs = "console=ttyS0,115200n8 reboot=k panic=1 pci=off " +
	"nmi_watchdog=0 hung_task_timeout_secs=0 " +
	// BuildKit generates a per-VM proxy CA during worker startup. The
	// Firecracker guest has no boot-time user input, so explicitly allow the
	// kernel CPU RNG and give virtio-rng maximum credit; otherwise getrandom(2)
	// can remain blocked indefinitely while BuildKit generates its RSA key.
	"random.trust_cpu=on rng_core.default_quality=1000 " +
	"root=/dev/vda ro " +
	"ip=10.0.0.2::10.0.0.1:255.255.255.252::eth0:off init=/sbin/init"

// ColdBootSpec is everything needed to build a cold-boot VM config. RAM and vCPU
// come from the app's plan (via pkg/api limits) — never inline them here.
//
// Issue #96 / ADR-025 axis 2 (PR #116): KernelKey / BaseKey / LayerKey are
// the StorageBackend keys (e.g. "kernel/<fcVersion>",
// "base/runtime-node22.ext4", "apps/<slug>/<depID>.ext4") that vmmd resolves
// through Storage.Get before staging into the jail chroot. The local
// backend's Get maps keys to /srv/fc/.../...ext4 the same way the legacy
// *Path fields did, so single-box behaviour is preserved; the OCI backend
// fetches over HTTP, which is what unblocks multi-node cold-boot (issue
// #98). Field names changed from *Path → *Key to make the type-system
// match the new semantics; the field type is still string so existing
// call sites only need a name update.
type ColdBootSpec struct {
	KernelKey  string // StorageBackend key (e.g. "kernel/1.10.0")
	BaseKey    string // StorageBackend key for drive0 shared ro base rootfs
	LayerKey   string // StorageBackend key for drive1 per-app app layer (legacy single-workload path)
	VcpuCount  int    // 2, or 4 for Scale
	MemSizeMiB int    // plan RAM
	Tap        string // netns-side tap device (always "tap0")
	// HealthcheckPath (issue #460 / ADR-053, ADR-057 / PR-D) is the
	// per-deployment override readiness probe path. Empty = legacy
	// TCP-accept on :8080 (pre-PR-D default). Non-empty → waitReady
	// does HTTP GET <HealthcheckPath> against <HostIP>:8080 and
	// accepts 2xx as ready. Forwarded from WakeRequest.HealthcheckPath
	// by Manager.bringUp.
	HealthcheckPath string
	// StartupDeadlineS is the per-app readiness budget. 0 means use the
	// vmmd default, preserving direct callers from before M-3.
	StartupDeadlineS int
	// SkipReady suppresses readiness probing for builder VMs. Builder guests
	// run a finite build and power off instead of binding port 8080.
	SkipReady bool
	// Workloads (issue #463 / ADR-069 / PR-B) is the per-workload
	// drive set. Non-empty → BootColdBoot emits one FC Drive per
	// entry (in addition to the base drive); the manager threads
	// Workloads[0] as the main workload's drive1 and
	// Workloads[1..N] as sidecar drives. Empty = legacy
	// single-workload path (LayerKey above), pre-PR-B callers.
	// The base drive is always the first DriveID; Workloads do
	// NOT replace it. Additive per ADR-016.
	Workloads []WorkloadSpec
	// SecretsEnvJSON and APIEnvJSON are prepared by Manager.Wake and staged
	// into drive1 before Firecracker receives its config. Keeping the payload
	// on the boot spec closes the race where guest-init starts the workload
	// before a late host-side loopback write.
	SecretsEnvJSON []byte
	APIEnvJSON     []byte
}

// JobColdBootSpec (issue #1184 Workstream A / ADR-099) is the
// per-job-task cold-boot payload. Mirrors ColdBootSpec for the
// run-to-completion workload class; the key differences are:
//
//   - ImageRef is the customer-specified OCI digest (NOT the app's
//     pre-built layer). The manager resolves it via the storage
//     backend on first cold-boot; subsequent runs reuse the
//     staged layer (same as app cold-boot path).
//   - Command is the argv (exec form, no shell). guest/init/
//     job_supervisor_linux.go (M8) does the syscall.Exec.
//   - Env is merged into the guest's process env: systemEnv ⊕
//     job.env_overrides ⊕ run.env_overrides (run overrides win).
//   - TaskTimeoutSec is the per-task wall-clock cap that the
//     guest supervisor enforces (via SIGTERM → 30s grace →
//     SIGKILL) and that schedd uses to compute lease_expires_at.
//   - LeaseToken is the idempotency key for the post-exit DGRAM
//     (HandleJobExit rejects tokens that don't match the row).
//   - VsockJobExitPort / VsockJobExitMsgType are hard-coded; the
//     guest-init supervisor reads them via /etc/faas/app.json
//     (mirrors the characterize-port load path).
//   - No HealthcheckPath / SkipReady: jobs run a single command
//     to completion, not a long-lived listener. The supervisor
//     exits as soon as the command exits; HandleJobExit fires
//     off the DGRAM.
//
// EffectiveDestroyWait is min(task_timeout_s + 90s,
// JobDestroyWaitDefault) so a long-running job's cleanup phase
// (SIGTERM → 30s grace → SIGKILL → poweroff) fits inside the
// firecracker destroy budget. See pkg/fcvm/vmm.go::JobDestroyWaitDefault.
type JobColdBootSpec struct {
	KernelKey  string
	BaseKey    string
	ImageRef   string
	Command    []string
	Env        map[string]string
	VcpuCount  int
	MemSizeMiB int
	Tap        string
	// TaskTimeoutSec is the per-task wall-clock cap (already
	// validated against api.JobTaskTimeoutSec[plan]).
	TaskTimeoutSec int
	// LeaseToken is the (run_id|"\x00"|task_index) lease from
	// Engine.WakeJob. The guest supervisor embeds it in the
	// job_exit DGRAM payload so HandleJobExit can verify ownership.
	LeaseToken string
	// AccountID + RunID + TaskIndex are stamped into
	// /etc/faas/app.json for guest-init introspection (mirrors
	// the app manifest shape).
	AccountID string
	RunID     string
	TaskIndex int
}

// BuildColdBootConfig assembles the Firecracker config for a cold boot. MMDS and
// balloon are off in v1 (spec §4.4); virtio-rng is always attached (spec §11).
//
// slot is the per-instance slot from Lease.Slot; it must be in range [0, MaxSlots).
// It derives GuestVsockCID so the in-guest resume listener is reachable at a
// globally unique vsock address (ADR-022). The Manager passes 0 when the slot
// is not yet known (test seams); production always passes the real slot.
//
// Drive layout (issue #463 / ADR-069 / PR-B):
//
//   - DriveBase (drive0, RO, root): shared read-only base.
//   - DriveLayerMain (drive1, RW): the main workload's drive1.
//   - DriveSidecarPrefix + index (drive2..N, RO): sidecar drive1s.
//
// PR-B additive: when ColdBootSpec.Workloads is empty, the
// legacy single-workload shape (DriveBase + DriveLayer) is
// emitted unchanged. When Workloads is non-empty, the manager
// pre-resolved the StorageBackend keys via materializeFromStorage
// so PathOnHost already points at the staged tmp files (the
// caller fills PathOnHost from BootColdBoot's resolution loop).
//
// Sidecar drives are read-only because each sidecar's per-workload
// upper is the shared rw overlay (ADR-069 §"no shared writable
// layer between workloads" — there is one upper for the whole
// guest, and per-workload writes from main + sidecars coalesce
// there; the load-bearing property is that no sidecar gets a
// second writable layer it could use to escape quota accounting).
// The main drive stays RW so the customer's container can
// write to /tmp, install pip packages, etc.
func BuildColdBootConfig(s ColdBootSpec, slot int) VMConfig {
	drives := []Drive{
		{DriveID: DriveBase, PathOnHost: s.BaseKey, IsRootDevice: true, IsReadOnly: true},
	}
	if len(s.Workloads) == 0 {
		// Legacy single-workload path. DriveLayer is the
		// alias for DriveLayerMain (see const block).
		drives = append(drives, Drive{DriveID: DriveLayer, PathOnHost: s.LayerKey, IsRootDevice: false, IsReadOnly: false})
	} else {
		// PR-B: one drive per workload, in spec order.
		// Workloads[0] is always the main workload
		// (DriveLayerMain, RW); Workloads[1..N] are
		// sidecars (DriveSidecarPrefix+idx, RO).
		//
		// BootColdBoot pre-resolves each StorageBackend key
		// via materializeFromStorage and overwrites the
		// StorageKey field with the staged tmp path before
		// calling BuildColdBootConfig. Tests that bypass
		// BootColdBoot (Boot with an already-resolved
		// VMConfig) keep the StorageKey semantics intact
		// — BuildColdBootConfig doesn't touch Storage.Get.
		for i, w := range s.Workloads {
			driveID := w.DriveID
			if driveID == "" {
				if i == 0 {
					driveID = DriveLayerMain
				} else {
					driveID = fmt.Sprintf("%s%d", DriveSidecarPrefix, i-1)
				}
			}
			drives = append(drives, Drive{
				DriveID:      driveID,
				PathOnHost:   w.StorageKey,
				IsRootDevice: false,
				IsReadOnly:   i != 0, // main RW; sidecars RO
			})
		}
	}
	return VMConfig{
		BootSource:        BootSource{KernelImagePath: s.KernelKey, BootArgs: coldBootArgs},
		Drives:            drives,
		MachineConfig:     Machine{VcpuCount: s.VcpuCount, MemSizeMib: s.MemSizeMiB, Smt: false},
		NetworkInterfaces: []NetIface{{IfaceID: "eth0", HostDevName: s.Tap}},
		Entropy:           &Entropy{},
		VsockDevice:       NewVsockDevice(slot),
		EphemeralWritable: s.SkipReady,
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
//
// Issue #463 / ADR-069 / PR-B: LayerKey is optional when Workloads
// is non-empty — the main workload's StorageBackend key lives on
// Workloads[0].StorageKey instead. The legacy single-workload
// path (no Workloads) still requires LayerKey; a mixed shape
// (LayerKey + Workloads) is rejected because callers must not
// specify the main workload twice.
func (s ColdBootSpec) Validate() error {
	switch {
	case s.KernelKey == "":
		return fmt.Errorf("fcvm: cold boot: empty kernel key")
	case s.BaseKey == "":
		return fmt.Errorf("fcvm: cold boot: empty base rootfs key")
	case len(s.Workloads) == 0 && s.LayerKey == "":
		return fmt.Errorf("fcvm: cold boot: empty app-layer key")
	case len(s.Workloads) > 0 && s.LayerKey != "":
		return fmt.Errorf("fcvm: cold boot: LayerKey must be empty when Workloads is set")
	case s.VcpuCount < 1:
		return fmt.Errorf("fcvm: cold boot: vcpu_count %d < 1", s.VcpuCount)
	case s.MemSizeMiB < 1:
		return fmt.Errorf("fcvm: cold boot: mem_size_mib %d < 1", s.MemSizeMiB)
	case s.Tap == "":
		return fmt.Errorf("fcvm: cold boot: empty tap device")
	case s.StartupDeadlineS < 0:
		return fmt.Errorf("fcvm: cold boot: startup_deadline_s %d < 0", s.StartupDeadlineS)
	}
	return nil
}

// Jailer paths (spec §8, Appendix B).
const (
	JailChrootBase = "/srv/fc/jail"
	// ParentCgroupRoot is the logical systemd slice name used by the
	// legacy helpers and documentation. The cgroup-v2 filesystem path
	// includes the enclosing faas.slice; CgroupMountRoot carries that
	// fully-qualified path for production jailer/cgroup operations.
	// Issue #301 / ADR-044: prior to this PR, every VM landed directly
	// at faas-tenant.slice/<instance> with a single neutral cpu.weight=256;
	// the new design nests under the per-plan sub-slice so the kernel
	// can enforce cpu.weight + cpu.max per plan tier.
	ParentCgroupRoot = "faas-tenant.slice"
	CgroupMountRoot  = "faas.slice/faas-tenant.slice"
	// BuilderCgroupParent is the systemd-owned build slice nested under
	// faas-cp.slice. Keeping builders below vmmd's delegated service slice
	// lets the privileged supervisor apply the per-build fence after jailer
	// creates the instance scope.
	BuilderCgroupParent = "faas.slice/faas-cp.slice/faas-cp-build.slice"
	// defaultParentCgroup is the legacy 2-level hierarchy (free / hobby /
	// pro / scale unaware) — kept as a non-empty fallback for callers
	// that don't pass a plan through (e.g. unit tests that mock the
	// Manager without schedd). Production always passes a real plan;
	// the fallback emits a warning and lands the instance directly
	// under ParentCgroupRoot so the test still runs. Use
	// ParentCgroupFor(plan) for the production path.
	//
	// Historical name: this constant USED to be called "ParentCgroup".
	// The rename to ParentCgroupRoot + ParentCgroupFor(plan) split is
	// what introduced the per-plan sub-slice hierarchy (issue #301 /
	// ADR-044). Direct callers that previously read the const must be
	// updated — a code-review tripwire would be nice but we don't have
	// one; rely on the package-wide grep for "ParentCgroup(".
	defaultParentCgroup = CgroupMountRoot
	FirecrackerBin      = "firecracker"
	APISockName         = "api.sock"
	VMConfigName        = "vmconfig.json"
	// VsockUDSSocketName is the chroot-relative path Firecracker creates the
	// vsock UDS on when a vsock device is attached. Jailer owns it under
	// the per-instance chroot; the host side dials through chrootRoot.
	VsockUDSSocketName = "vsock.sock"
	// VsockDeviceID is the Firecracker device id used in /vsock PUT bodies
	// and referenced from the config-file.
	VsockDeviceID = "vsock-0"
)

// ParentCgroupFor returns the full parent-cgroup path the jailer
// should attach a VM to. The 3-level hierarchy (issue #301, ADR-044)
//
//	/sys/fs/cgroup/faas-tenant.slice/<api.Plan.SliceName>/<instance>
//
// puts each instance under the per-plan sub-slice systemd drops at
// boot (deploy/ansible/roles/systemd_slices), so the kernel can
// enforce cpu.weight (the sub-slice's CPUWeight= directive) and
// cpu.max (the per-instance direct write in writePlanCgroup) per
// plan tier.
//
// Empty plan falls back to the legacy 2-level path so unit tests
// that don't thread a plan through (and pre-issue-301 callers) keep
// working. Production never passes an empty plan — the Manager.Wake
// validation rejects a missing plan (see WakeRequest.Plan doc).
func ParentCgroupFor(plan api.Plan) string {
	slice := plan.SliceName()
	if slice == "" {
		return defaultParentCgroup
	}
	return CgroupMountRoot + "/faas-" + slice + ".slice"
}

// LegacyParentCgroupFor returns the accidentally double-prefixed per-plan
// path used before the usage telemetry fix. It is read-only compatibility for
// rolling upgrades: new jailers must use ParentCgroupFor, while samplers keep
// discovering VMs created by an older vmmd until those VMs are recycled.
func LegacyParentCgroupFor(plan api.Plan) string {
	slice := plan.SliceName()
	if slice == "" {
		return defaultParentCgroup
	}
	return CgroupMountRoot + "/faas-tenant-" + slice + ".slice"
}

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
	// Plan is the apps row's owning plan tier (issue #301, ADR-044).
	// Drives the parent-cgroup path (ParentCgroupFor) and the
	// --cgroup cpu.weight=N argv so the kernel enforces the per-plan
	// share. Required by JailerCommand — an empty plan falls back to
	// the neutral cpu.weight=256 default (legacy 2-level path) so
	// pre-issue-301 callers don't break, but production always sets
	// it from WakeRequest.Plan.
	Plan api.Plan
	// IsBuilder routes the jailer to faas-cp-build.slice and uses a neutral
	// cgroup weight instead of a tenant plan weight.
	IsBuilder bool
	// MemoryMaxBytes is passed as a cgroup creation parameter. Zero preserves
	// the legacy/test command shape; production Manager.Wake supplies the
	// billable VM ceiling before jailer drops privileges.
	MemoryMaxBytes int64
}

// PerInstanceScope returns the cgroup scope name the jailer will create
// at <parent_cgroup>/<scope>. The name MUST be a strict subset of
// jailer v1.7's --id character whitelist (alnum + `-` + `_`); the
// doc-comment on JailerCommand names those chars and how we choose a
// safe value. The vmm wrapper looks up memory.max and on-Kill removes
// exactly this path; matching the jailer's choice keeps the two in
// step.
//
// Kept as a free function so pkg/fcvm/cgroup.go and vmm.go can both
// reach it without dragging the rest of JailerSpec into scope.
func PerInstanceScope(instance string) string { return instance }

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
// --id is set to PerInstanceScope(s.Instance) — currently just the
// instance name, because jailer v1.7 rejects '.' and other special
// characters in --id (panic: "Invalid char (.) at position N"). The
// vmm wrapper writes memory.max into
// `<cgroupRoot>/faas-tenant.slice/<plan-slice>/<instance>` after
// bringUp; the scope must exist by then or writeMemoryMax returns
// IsNotExist. Note this means the cgroup scope, chroot leaf, and
// AF_VSOCK jailer's --id are all the same string — keep them in
// lockstep.
//
// --cgroup cpu.weight=N is mandatory on the v2 path: without at
// least one --cgroup-param, jailer (FC v1.7+) only attaches the
// jailer PID to the parent slice and never creates a per-instance
// child scope. The value is plan-driven (issue #301, ADR-044):
// Free=2 / Hobby=4 / Pro=8 / Scale=16. An empty plan falls back
// to the legacy cpu.weight=256 neutral default so pre-issue-301
// callers keep working. The cpu.max quota is written by the vmm
// wrapper in writePlanCgroup because jailer v1.7 has no
// --cgroup cpu.max= arg — cpu.weight and memory.max are exposed through
// --cgroup; the CPU quota is applied by the vmmd wrapper after boot.
func JailerCommand(s JailerSpec) []string {
	execFile := s.ExecFile
	if execFile == "" {
		execFile = FirecrackerBin
	}
	parentCgroup := ParentCgroupFor(s.Plan)
	cpuWeight := s.Plan.CPUWeight()
	if s.IsBuilder {
		parentCgroup = BuilderCgroupParent
		cpuWeight = 256
	}
	if cpuWeight <= 0 {
		// Empty plan: keep the legacy neutral default. cpu.weight
		// must always be in [1, 10000] per the kernel; 256 is the
		// mid of the normalised range.
		cpuWeight = 256
	}
	args := []string{
		"jailer",
		"--id", PerInstanceScope(s.Instance),
		"--uid", fmt.Sprintf("%d", s.UID),
		"--gid", fmt.Sprintf("%d", s.GID),
		"--exec-file", execFile,
		"--chroot-base-dir", JailChrootBase,
		"--netns", "/run/netns/" + s.Netns,
		"--cgroup-version", "2",
		"--parent-cgroup", parentCgroup,
		"--cgroup", fmt.Sprintf("cpu.weight=%d", cpuWeight),
	}
	if s.MemoryMaxBytes > 0 {
		args = append(args, "--cgroup", fmt.Sprintf("memory.max=%d", s.MemoryMaxBytes))
	}
	args = append(args,
		"--",
		"--api-sock", APISockName,
	)
	return args
}

// WorkloadSpec (issue #463 / ADR-069 / PR-B; issue #463 / PR-C §6
// adds cmd/entrypoint) is one workload's per-drive shape. The main
// workload is Workloads[0]; sidecars are Workloads[1..N]. Each
// entry carries the StorageBackend key for the drive's ext4 + the
// FC Drive.DriveID vmmd mounts inside the jail chroot + the
// workload's cgroup RAM ceiling.
//
// Name is the customer-chosen sidecar name (lowercase alpha-num
// plus dash, max 63 chars) — main is the literal "main".
// Type is "init", "sidecar", or "main" (the closed enum from
// api.SidecarType). The effective sidecar command and image-default
// environment are baked into /etc/faas/workloads/<name>/workload.json;
// sealed env overrides are carried separately and staged into the
// main workload's writable layer at wake. Cmd and Entrypoint below
// are only a compatibility fallback for older sidecar layers. Port
// remains on the wire because it is deployment scheduling metadata.
//
// RamMB is the per-workload cgroup memory.max. 0 = "absent /
// inherit the plan RAM" (the common case for the main workload).
//
// Cmd and Entrypoint (PR-C §6) are the customer-image override
// surface. Empty (the default) means "use the baked image
// entrypoint": /usr/local/bin/start.sh for sidecars, the main
// workload's baked entrypoint for the main workload. When non-empty,
// guest-init exec's Entrypoint[0] with Entrypoint[1:] as argv[0:]
// and Cmd as the append argv. Pattern mirrors the OCI image-spec
// shape so a deploy that ships a Dockerfile (or imaged) has a
// single canonical way to override the entrypoint without stamping
// a new base layer.
type WorkloadSpec struct {
	Name       string // "main" for the main workload; sidecar name for the rest
	Type       string // "main", "init", "sidecar"
	Image      string // digest-pinned sidecar image, retained for wire/audit parity
	StorageKey string // StorageBackend key (apps/<slug>/<depID>[-<name>].ext4)
	DriveID    string // FC Drive.DriveID (DriveLayerMain / DriveSidecarPrefix+idx)
	RamMB      int    // 0 = inherit plan RAM
	Port       int    // 0 = inherit main port (8080)
	Essential  bool   // type=="init" + essential=true → fail deploy on non-zero exit
	Cmd        []string
	Entrypoint []string
	// SealedEnv carries per-sidecar ciphertext from the deployment record. It is
	// unsealed by Manager.Wake and never written into the shared sidecar image.
	SealedEnv []SealedEnvEntry
	// preparedEnvJSON is the per-instance plaintext env file produced by
	// Manager.Wake. It is intentionally internal so plaintext cannot cross the
	// scheduler/vmmd wire or be accidentally serialized as part of WorkloadSpec.
	preparedEnvJSON []byte
}
