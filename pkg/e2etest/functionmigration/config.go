// Package functionmigration defines the guarded native migration fixture and
// its successful-migration evidence. It does not enable production execution.
package functionmigration

// adr: 1547

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

type Config struct {
	Version       int               `json:"version"`
	FixtureID     string            `json:"fixture_id"`
	Revision      string            `json:"revision"`
	Catalog       string            `json:"catalog"`
	APIURL        string            `json:"api_url"`
	GatewayURL    string            `json:"gateway_url"`
	AppDomain     string            `json:"app_domain"`
	SQLIP         string            `json:"sql_ip"`
	SQLCAPath     string            `json:"sql_ca_path"`
	FCVersion     string            `json:"fc_version"`
	Builds        map[string]string `json:"builds"`
	TestBinarySHA string            `json:"test_binary_sha256"`
	Hosts         []Host            `json:"hosts"`
}

type Host struct {
	NodeID       string            `json:"node_id"`
	SSH          string            `json:"ssh"`
	RemoteSocket string            `json:"remote_socket"`
	LocalSocket  string            `json:"local_socket"`
	Assets       map[string]string `json:"assets"`
	Proof        HostProof         `json:"proof"`
}

type HostProof struct {
	Version       int               `json:"version"`
	FixtureID     string            `json:"fixture_id"`
	Revision      string            `json:"revision"`
	Catalog       string            `json:"catalog"`
	NodeID        string            `json:"node_id"`
	Root          bool              `json:"root"`
	NativeX86     bool              `json:"native_x86"`
	KVM           bool              `json:"kvm"`
	MachineSHA    string            `json:"machine_sha256"`
	BootSHA       string            `json:"boot_sha256"`
	VMMDPID       int               `json:"vmmd_pid"`
	ScheddPID     int               `json:"schedd_pid"`
	VMMDStart     string            `json:"vmmd_start"`
	ScheddStart   string            `json:"schedd_start"`
	RunningVMMD   string            `json:"running_vmmd_sha256"`
	RunningSchedd string            `json:"running_schedd_sha256"`
	Assets        map[string]string `json:"assets"`
}

func AssetNames() []string {
	return []string{"vmmd", "schedd", "guest_init", "node22", "node24", "kernel", "builder_base", "firecracker", "leakcheck", "leakcheck_native_loops"}
}

func Load(path string) (Config, error) {
	var c Config
	data, err := os.ReadFile(path)
	if err != nil {
		return c, errors.New("native migration configuration is unreadable")
	}
	if len(data) > 1<<20 {
		return c, errors.New("native migration configuration is too large")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || !errors.Is(d.Decode(new(any)), io.EOF) {
		return c, errors.New("native migration configuration is invalid")
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	ip, err := netip.ParseAddr(c.SQLIP)
	if c.Version != 1 || !validUUID(c.FixtureID) || !hexBytes(c.Revision, 20) || c.Catalog != "faas_function_migration_native_"+strings.ReplaceAll(c.FixtureID, "-", "") ||
		!localURL(c.APIURL) || !localURL(c.GatewayURL) || c.AppDomain == "" || strings.ContainsAny(c.AppDomain, " /:@\r\n\x00") ||
		err != nil || !ip.Is4() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
		!absolutePath(c.SQLCAPath) || c.FCVersion == "" || !hexBytes(c.TestBinarySHA, 32) || len(c.Hosts) != 2 || len(c.Builds) != len(AssetNames()) {
		return errors.New("native migration fixture scope or prerequisites are invalid")
	}
	for _, name := range AssetNames() {
		if !hexBytes(c.Builds[name], 32) {
			return errors.New("native migration build hashes are incomplete")
		}
	}
	for _, h := range c.Hosts {
		if err := c.checkHost(h); err != nil {
			return err
		}
	}
	a, b := c.Hosts[0], c.Hosts[1]
	if a.NodeID == b.NodeID || a.LocalSocket == b.LocalSocket || a.Proof.MachineSHA == b.Proof.MachineSHA || a.Proof.BootSHA == b.Proof.BootSHA {
		return errors.New("native migration requires two distinct physical hosts and nodes")
	}
	return nil
}

func (c Config) checkHost(h Host) error {
	p := h.Proof
	if !validUUID(h.NodeID) || h.SSH == "" || strings.HasPrefix(h.SSH, "-") || strings.ContainsAny(h.SSH, " \t\r\n\x00") ||
		!absolutePath(h.RemoteSocket) || !absolutePath(h.LocalSocket) || len(h.Assets) != len(AssetNames()) ||
		p.Version != 1 || p.FixtureID != c.FixtureID || p.Revision != c.Revision || p.Catalog != c.Catalog || p.NodeID != h.NodeID ||
		!p.Root || !p.NativeX86 || !p.KVM || !hexBytes(p.MachineSHA, 32) || !hexBytes(p.BootSHA, 32) || p.VMMDPID <= 0 || p.ScheddPID <= 0 || !processStart(p.VMMDStart) || !processStart(p.ScheddStart) ||
		p.RunningVMMD != c.Builds["vmmd"] || p.RunningSchedd != c.Builds["schedd"] || len(p.Assets) != len(AssetNames()) {
		return errors.New("native migration host identity or running binaries differ")
	}
	for _, name := range AssetNames() {
		if !absolutePath(h.Assets[name]) || p.Assets[name] != c.Builds[name] {
			return errors.New("native migration host assets differ from this source build")
		}
	}
	return nil
}

func validUUID(s string) bool {
	u, err := uuid.Parse(s)
	return err == nil && u != uuid.Nil && u.String() == s
}

func hexBytes(s string, n int) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == n && strings.ToLower(s) == s && s != strings.Repeat("0", n*2)
}

func processStart(s string) bool {
	start, err := strconv.ParseUint(s, 10, 64)
	return err == nil && start > 0 && strconv.FormatUint(start, 10) == s
}

func absolutePath(s string) bool {
	return filepath.IsAbs(s) && filepath.Clean(s) == s && !strings.ContainsAny(s, "\x00\r\n\t :")
}

func localURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.Port() == "" {
		return false
	}
	ip, err := netip.ParseAddr(u.Hostname())
	return err == nil && ip.IsLoopback()
}
