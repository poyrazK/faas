package edgetopology

// adr: 706

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestNativeTCPParsesNativeEndianIPv4IPv6AndRejectsMalformedTables(t *testing.T) {
	for _, address := range []string{"0.0.0.0:443", "127.0.0.1:8080", "192.0.2.10:443", "[::]:443", "[2001:db8::10]:443"} {
		ipv6 := strings.HasPrefix(address, "[")
		got, err := parseNativeTCP([]byte(nativeTCPHeader+nativeTCPLine(address, 55, "0A")), ipv6)
		if err != nil || len(got) != 1 || got[0].Address != address || got[0].Inode != 55 || !got[0].Listening {
			t.Fatal(got, err)
		}
	}
	good := nativeTCPHeader + nativeTCPLine("127.0.0.1:8080", 55, "0A")
	for name, body := range map[string]string{
		"empty": "", "truncated": strings.TrimSuffix(good, "\n"), "header": strings.Replace(good, "local_address", "foreign_address", 1),
		"row":   "sl local_address rem_address st tx_queue rx_queue tr tm->when retrnsmt uid timeout inode\n0: bad\n",
		"state": strings.Replace(good, " 0A ", " 00 ", 1), "zero-inode": nativeTCPHeader + nativeTCPLine("127.0.0.1:8080", 0, "0A"),
		"zero-port": nativeTCPHeader + nativeTCPLine("127.0.0.1:0", 55, "0A"), "wrong-family": nativeTCPHeader + nativeTCPLine("[::]:443", 55, "0A"),
		"foreign-peer": strings.Replace(good, "00000000:0000", "0100007F:1234", 1),
		"byte-bound":   strings.Repeat("x", api.RuntimeUpgradeNativeProcMaxBytes+1),
		"row-bound":    nativeTCPHeader + strings.Repeat(nativeTCPLine("127.0.0.1:8080", 55, "01"), api.RuntimeUpgradeNativeTCPRowLimit+1),
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := parseNativeTCP([]byte(body), false); !errors.Is(err, ErrNativeUnverified) || len(got) != 0 {
				t.Fatal(got, err)
			}
		})
	}
	if got, err := parseNativeTCP([]byte(nativeTCPHeader+nativeTCPLine("[::ffff:192.0.2.10]:443", 55, "0A")), true); err == nil || len(got) != 0 {
		t.Fatal(got, err)
	}
}

func TestNativeServiceParsersRejectAmbiguousCredentialsAndProperties(t *testing.T) {
	s := nativeTestScope("127.0.0.1:8080", "127.0.0.1:2019").Services[0]
	unit, stat := string(nativeUnitBody(s)), string(nativeStatBody(s))
	if matchNativeUnit([]byte(unit), s) != nil || matchNativeStat([]byte(stat), s.PID, s.StartTicks) != nil || matchNativeUID([]byte("Name:\tfixture\nUid:\t997\t997\t997\t997\n"), s.UID) != nil {
		t.Fatal("valid parser fixture refused")
	}
	for name, body := range map[string]string{
		"duplicate": unit + "Id=caddy.service\n", "missing": strings.ReplaceAll(unit, "LoadState=loaded\n", ""), "extra": unit + "Unexpected=hidden\n",
		"alias": strings.ReplaceAll(unit, "Id=caddy.service", "Id=other.service"), "oversized": strings.Repeat("x", api.RuntimeUpgradeNativeUnitMaxBytes+1), "blank": unit + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			if matchNativeUnit([]byte(body), s) == nil {
				t.Fatal(body)
			}
		})
	}
	for _, body := range []string{strings.Replace(stat, "101 (", "102 (", 1), strings.Replace(stat, " S ", " Z ", 1), strings.Replace(stat, "456", "0456", 1), "101 (x) S 0\n"} {
		if matchNativeStat([]byte(body), s.PID, s.StartTicks) == nil {
			t.Fatal(body)
		}
	}
	for _, body := range []string{"Uid:\t997\t997\t997\t0\n", "Uid:\t0997\t997\t997\t997\n", "Uid:\t997\t997\t997\n", "Uid:\t997\t997\t997\t997\nUid:\t997\t997\t997\t997\n", "Name:\tx\n"} {
		if matchNativeUID([]byte(body), s.UID) == nil {
			t.Fatal(body)
		}
	}
}

func TestNativeTCPOverlapAndOriginCoverageAreConservativeAcrossFamilies(t *testing.T) {
	for _, c := range []struct {
		a, b    string
		overlap bool
	}{
		{"127.0.0.1:8080", "0.0.0.0:8080", true}, {"[::]:8080", "127.0.0.1:8080", true},
		{"[::1]:8080", "127.0.0.1:8080", false}, {"127.0.0.1:8080", "127.0.0.2:8080", false}, {"0.0.0.0:443", "0.0.0.0:80", false},
	} {
		if nativeTCPOverlap(c.a, c.b) != c.overlap || nativeTCPOverlap(c.b, c.a) != c.overlap {
			t.Fatal(c)
		}
	}
	for _, c := range []struct {
		configured, native, ip string
		covered                bool
	}{
		{":443", "0.0.0.0:443", "192.0.2.10", true}, {":443", "[::]:443", "2001:db8::10", true},
		{":443", "[::]:443", "192.0.2.10", false}, {"[::]:443", "0.0.0.0:443", "192.0.2.10", false},
		{"192.0.2.10:443", "192.0.2.10:443", "192.0.2.10", true}, {"0.0.0.0:443", "192.0.2.10:443", "192.0.2.10", false},
		{":443", "192.0.2.11:443", "192.0.2.10", false}, {":80", "0.0.0.0:443", "192.0.2.10", false},
	} {
		if nativeListenerCovers(c.configured, c.native, c.ip) != c.covered {
			t.Fatal(c)
		}
	}
}

func TestNativeScopeReviewIsCopySafeAndRejectsInvalidIdentityAndLimits(t *testing.T) {
	input := nativeTestScope("127.0.0.1:8080", "127.0.0.1:2019")
	frozen, err := freezeNativeScope(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Host.Addresses[0].IP = "changed"
	input.Services[0].TCPListeners[0] = "changed"
	if frozen.Host.Addresses[0].IP == "changed" || slices.Contains(frozen.Services[0].TCPListeners, "changed") {
		t.Fatal(frozen)
	}
	cases := map[string]func(*NativeScopeReview){
		"machine-id": func(r *NativeScopeReview) { r.Host.MachineID = strings.Repeat("0", 32) },
		"boot":       func(r *NativeScopeReview) { r.Host.BootID = "BAD" }, "ns": func(r *NativeScopeReview) { r.Host.NetNamespace = "net:[001]" },
		"mapped-ip": func(r *NativeScopeReview) { r.Host.Addresses[0].IP = "::ffff:192.0.2.10" },
		"interface": func(r *NativeScopeReview) { r.Host.Addresses[0].Interface = "../eth0" }, "prefix": func(r *NativeScopeReview) { r.Host.Addresses[0].Prefix = 33 },
		"duplicate-ip": func(r *NativeScopeReview) { r.Host.Addresses = append(r.Host.Addresses, r.Host.Addresses[0]) }, "missing-ip": func(r *NativeScopeReview) { r.Host.Addresses = nil },
		"unit": func(r *NativeScopeReview) { r.Services[0].Unit = "--remote.service" }, "pid1": func(r *NativeScopeReview) { r.Services[0].PID = 1 }, "duplicate-pid": func(r *NativeScopeReview) { r.Services[1].PID = r.Services[0].PID },
		"zero-start": func(r *NativeScopeReview) { r.Services[0].StartTicks = 0 }, "invocation": func(r *NativeScopeReview) { r.Services[0].InvocationID = strings.Repeat("A", 32) },
		"hash": func(r *NativeScopeReview) { r.Services[0].ExeSHA256 = "bad" }, "cgroup-escape": func(r *NativeScopeReview) { r.Services[0].Cgroup = "/system.slice/../caddy.service" },
		"cgroup-unit": func(r *NativeScopeReview) { r.Services[0].Cgroup = "/system.slice/other.service" }, "cgroup-inode": func(r *NativeScopeReview) { r.Services[0].CgroupInode = 0 },
		"hostname": func(r *NativeScopeReview) { r.Services[0].TCPListeners[0] = "localhost:443" }, "port": func(r *NativeScopeReview) { r.Services[0].TCPListeners[0] = "127.0.0.1:0443" },
		"duplicate-listener": func(r *NativeScopeReview) { r.Services[1].TCPListeners[0] = r.Services[0].TCPListeners[0] }, "service-bound": func(r *NativeScopeReview) {
			r.Services = make([]NativeServiceReview, api.RuntimeUpgradeNativeServiceLimit+1)
		},
		"listener-bound": func(r *NativeScopeReview) {
			r.Services[0].TCPListeners = make([]string, api.RuntimeUpgradeNativeListenerLimit+1)
		}, "address-bound": func(r *NativeScopeReview) {
			r.Host.Addresses = make([]NativeHostAddress, api.RuntimeUpgradeNativeOriginLimit+1)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			review := nativeTestScope("127.0.0.1:8080", "127.0.0.1:2019")
			mutate(&review)
			if _, err := freezeNativeScope(review); !errors.Is(err, ErrNativeUnverified) {
				t.Fatal(err)
			}
		})
	}
}
