package sdunit

import (
	"net/netip"
	"strconv"

	"github.com/godbus/dbus/v5"
)

// property is one (sv) element of StartTransientUnit's property array.
type property struct {
	Name  string
	Value dbus.Variant
}

// auxUnit is one element of StartTransientUnit's auxiliary unit array; this
// package never sends any.
type auxUnit struct {
	Name  string
	Props []property
}

type execCommand struct {
	Path          string
	Args          []string
	IgnoreFailure bool
}

type ipPrefix struct {
	Family int32
	Addr   []byte
	Prefix uint32
}

type nameList struct {
	Allow bool
	Names []string
}

const (
	afInet  = 2  // AF_INET
	afInet6 = 10 // AF_INET6
)

func prop(name string, v any) property { return property{Name: name, Value: dbus.MakeVariant(v)} }

// properties is the service's StartTransientUnit property array. Besides the
// Service's own fields every unit gets the sandbox of ADR §2 "Unit
// properties": no capability and no way to gain one, a read-only system, no
// home, private /tmp and /dev, other processes invisible, @system-service
// syscalls only.
func (s Service) properties() []property {
	props := []property{
		prop("Description", s.Description),
		prop("Type", "exec"),
		prop("ExecStart", []execCommand{{Path: s.ExecStart[0], Args: s.ExecStart}}),
		prop("User", strconv.Itoa(s.UID)),
		prop("Group", strconv.Itoa(s.GID)),
		// A failed or stopped run is unloaded at once, so its name, cgroup and
		// state never linger.
		prop("CollectMode", "inactive-or-failed"),
		prop("NoNewPrivileges", true),
		prop("CapabilityBoundingSet", uint64(0)),
		prop("AmbientCapabilities", uint64(0)),
		prop("ProtectSystem", "strict"),
		prop("ProtectHome", "yes"),
		prop("PrivateTmp", true),
		prop("PrivateDevices", true),
		prop("ProtectProc", "invisible"),
		prop("ProtectKernelTunables", true),
		prop("ProtectKernelModules", true),
		prop("ProtectControlGroups", true),
		prop("LockPersonality", true),
		prop("RestrictRealtime", true),
		prop("RestrictSUIDSGID", true),
		prop("SystemCallArchitectures", []string{"native"}),
		prop("SystemCallFilter", nameList{Allow: true, Names: []string{"@system-service"}}),
	}
	if len(s.Environment) > 0 {
		props = append(props, prop("Environment", s.Environment))
	}
	if s.NetworkNamespacePath != "" {
		props = append(props, prop("NetworkNamespacePath", s.NetworkNamespacePath))
	}
	if s.Slice != "" {
		props = append(props, prop("Slice", s.Slice))
	}
	if s.MemoryMaxBytes > 0 {
		props = append(props, prop("MemoryAccounting", true), prop("MemoryMax", s.MemoryMaxBytes))
	}
	if s.TasksMax > 0 {
		props = append(props, prop("TasksAccounting", true), prop("TasksMax", s.TasksMax))
	}
	if s.LimitNOFILE > 0 {
		props = append(props, prop("LimitNOFILE", s.LimitNOFILE), prop("LimitNOFILESoft", s.LimitNOFILE))
	}
	if len(s.ReadWritePaths) > 0 {
		props = append(props, prop("ReadWritePaths", s.ReadWritePaths))
	}
	if len(s.IPAddressAllow) > 0 {
		allow := make([]ipPrefix, 0, len(s.IPAddressAllow))
		for _, p := range s.IPAddressAllow {
			allow = append(allow, ipPrefixOf(p))
		}
		props = append(props,
			prop("IPAddressAllow", allow),
			prop("IPAddressDeny", []ipPrefix{
				{Family: afInet, Addr: make([]byte, 4)},
				{Family: afInet6, Addr: make([]byte, 16)},
			}))
	}
	if len(s.RestrictAddressFamilies) > 0 {
		props = append(props, prop("RestrictAddressFamilies", nameList{Allow: true, Names: s.RestrictAddressFamilies}))
	}
	if s.TimeoutStop > 0 {
		props = append(props, prop("TimeoutStopUSec", uint64(s.TimeoutStop.Microseconds())))
	}
	return props
}

func ipPrefixOf(p netip.Prefix) ipPrefix {
	addr := p.Addr()
	if addr.Is4() {
		b := addr.As4()
		return ipPrefix{Family: afInet, Addr: b[:], Prefix: uint32(p.Bits())}
	}
	b := addr.As16()
	return ipPrefix{Family: afInet6, Addr: b[:], Prefix: uint32(p.Bits())}
}
