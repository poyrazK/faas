package sdunit

// adr: 20261017 connection survival, §2 unit properties (P1c)

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func bridgeService() Service {
	return Service{
		Description:             "bridge",
		ExecStart:               []string{"/opt/faas/bin/vmmd-stream-bridge", "/run/faas-ibridge/i/http.sock", "10.0.0.2", "8080", "24h"},
		Environment:             []string{"PATH=/usr/bin", "FAAS_BRIDGE_EXPECT_UID=30001"},
		UID:                     30001,
		GID:                     30001,
		NetworkNamespacePath:    "/run/netns/fc-i",
		Slice:                   "faas-tenant-ibridge.slice",
		MemoryMaxBytes:          32 << 20,
		TasksMax:                64,
		LimitNOFILE:             8256,
		ReadWritePaths:          []string{"/run/faas-ibridge/i"},
		IPAddressAllow:          []netip.Prefix{netip.MustParsePrefix("10.0.0.2/32")},
		RestrictAddressFamilies: []string{"AF_UNIX", "AF_INET"},
		TimeoutStop:             5 * time.Second,
	}
}

// Each property must carry the D-Bus type systemd's transient-unit parser
// expects; a wrong one fails StartTransientUnit with "Invalid argument".
func TestPropertiesCarrySystemdTypes(t *testing.T) {
	want := map[string]string{
		"Description": "s", "Type": "s", "ExecStart": "a(sasb)", "User": "s", "Group": "s",
		"CollectMode": "s", "NoNewPrivileges": "b", "CapabilityBoundingSet": "t", "AmbientCapabilities": "t",
		"ProtectSystem": "s", "ProtectHome": "s", "PrivateTmp": "b", "PrivateDevices": "b", "ProtectProc": "s",
		"ProtectKernelTunables": "b", "ProtectKernelModules": "b", "ProtectControlGroups": "b",
		"LockPersonality": "b", "RestrictRealtime": "b", "RestrictSUIDSGID": "b",
		"SystemCallArchitectures": "as", "SystemCallFilter": "(bas)", "Environment": "as",
		"NetworkNamespacePath": "s", "Slice": "s", "MemoryAccounting": "b", "MemoryMax": "t",
		"TasksAccounting": "b", "TasksMax": "t", "LimitNOFILE": "t", "LimitNOFILESoft": "t",
		"ReadWritePaths": "as", "IPAddressAllow": "a(iayu)", "IPAddressDeny": "a(iayu)",
		"RestrictAddressFamilies": "(bas)", "TimeoutStopUSec": "t",
	}
	props := bridgeService().properties()
	seen := map[string]bool{}
	for _, p := range props {
		if seen[p.Name] {
			t.Errorf("%s set twice", p.Name)
		}
		seen[p.Name] = true
		sig, ok := want[p.Name]
		if !ok {
			t.Errorf("unexpected property %s", p.Name)
			continue
		}
		if got := p.Value.Signature().String(); got != sig {
			t.Errorf("%s has type %s, want %s", p.Name, got, sig)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("property %s missing", name)
		}
	}
	if got := dbus.SignatureOf(props).String(); got != "a(sv)" {
		t.Fatalf("property array type %s, want a(sv)", got)
	}
	if got := dbus.SignatureOf([]auxUnit{}).String(); got != "a(sa(sv))" {
		t.Fatalf("auxiliary unit array type %s, want a(sa(sv))", got)
	}
}

func TestPropertiesValues(t *testing.T) {
	byName := map[string]any{}
	for _, p := range bridgeService().properties() {
		byName[p.Name] = p.Value.Value()
	}
	exec := byName["ExecStart"].([]execCommand)
	if len(exec) != 1 || exec[0].Path != "/opt/faas/bin/vmmd-stream-bridge" || len(exec[0].Args) != 5 || exec[0].Args[0] != exec[0].Path {
		t.Fatalf("ExecStart = %+v", exec)
	}
	if byName["User"] != "30001" || byName["Group"] != "30001" || byName["CapabilityBoundingSet"] != uint64(0) {
		t.Fatalf("identity = %v %v caps %v", byName["User"], byName["Group"], byName["CapabilityBoundingSet"])
	}
	allow := byName["IPAddressAllow"].([]ipPrefix)
	if len(allow) != 1 || allow[0].Family != afInet || string(allow[0].Addr) != string([]byte{10, 0, 0, 2}) || allow[0].Prefix != 32 {
		t.Fatalf("IPAddressAllow = %+v", allow)
	}
	deny := byName["IPAddressDeny"].([]ipPrefix)
	if len(deny) != 2 || deny[0].Prefix != 0 || deny[1].Prefix != 0 || len(deny[1].Addr) != 16 {
		t.Fatalf("IPAddressDeny = %+v, want any", deny)
	}
	if byName["TimeoutStopUSec"] != uint64(5_000_000) || byName["MemoryMax"] != uint64(32<<20) {
		t.Fatalf("timeout %v memory %v", byName["TimeoutStopUSec"], byName["MemoryMax"])
	}
}

// systemd rejects an Environment that assigns a name twice; the last
// assignment wins, as it would for exec.
func TestEnvironmentAssignsEachNameOnce(t *testing.T) {
	s := bridgeService()
	s.Environment = []string{"PATH=/a", "FAAS_BRIDGE_EXPECT_UID=30001", "PATH=/b", "FAAS_BRIDGE_PROTOCOL=h1", "EMPTY="}
	var env []string
	for _, p := range s.properties() {
		if p.Name == "Environment" {
			env = p.Value.Value().([]string)
		}
	}
	want := []string{"PATH=/b", "FAAS_BRIDGE_EXPECT_UID=30001", "FAAS_BRIDGE_PROTOCOL=h1", "EMPTY="}
	if strings.Join(env, "|") != strings.Join(want, "|") {
		t.Fatalf("Environment = %q, want %q", env, want)
	}
}

func TestServiceValidation(t *testing.T) {
	for name, mutate := range map[string]func(*Service){
		"no assignment": func(s *Service) { s.Environment = []string{"PATH"} },
		"bad name":      func(s *Service) { s.Environment = []string{"1PATH=/x"} },
		"empty name":    func(s *Service) { s.Environment = []string{"=/x"} },
		"relative exec": func(s *Service) { s.ExecStart = []string{"vmmd-stream-bridge"} },
		"no exec":       func(s *Service) { s.ExecStart = nil },
		"root":          func(s *Service) { s.UID, s.GID = 0, 0 },
		"invalid ip":    func(s *Service) { s.IPAddressAllow = []netip.Prefix{{}} },
	} {
		s := bridgeService()
		mutate(&s)
		if s.validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := bridgeService().validate(); err != nil {
		t.Fatal(err)
	}
}

func TestServiceNames(t *testing.T) {
	for name, ok := range map[string]bool{
		"faas-ibridge-0192f3c4-aaaa-7bbb-8ccc-0123456789ab-1a2b3c4d.service": true,
		"faas-ibridge-x.socket":    false,
		"faas ibridge.service":     false,
		"../x.service":             false,
		"faas-ibridge-\\x.service": false,
		".service":                 false,
	} {
		if ValidServiceName(name) != ok {
			t.Errorf("ValidServiceName(%q) = %v", name, !ok)
		}
	}
}

func TestErrorName(t *testing.T) {
	if errorName(dbus.Error{Name: errNoSuchUnit}) != errNoSuchUnit || errorName(&dbus.Error{Name: errNoSuchUnit}) != errNoSuchUnit {
		t.Fatal("D-Bus error names not read")
	}
	if errorName(errors.New("x")) != "" || errorName(nil) != "" {
		t.Fatal("non-D-Bus error named")
	}
}
