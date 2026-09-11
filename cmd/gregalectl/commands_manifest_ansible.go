package main

import (
	"bytes"
	"flag"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/manifest"
	"github.com/onebox-faas/faas/pkg/roleTemplating"
)

// manifestAnsibleFile is one deterministic generated artifact. Keeping the
// generation pure makes the command safe to dry-run and gives the tests a
// direct contract without invoking Ansible or touching a live host.
type manifestAnsibleFile struct {
	Path string
	Body []byte
}

type manifestInternalHost struct {
	Address       string
	InventoryHost string
	Names         []string
}

const manifestGatewayEgressPort = 9092
const manifestAppErrorsPort = 9093

// manifestPrivateHostsGroupVarsThreshold keeps the generated inventory
// compact as fleets grow. Below this point retaining the resolver map in each
// host_vars file preserves the small-fleet artifact shape; above it,
// the map is shared through inventory/group_vars/all.yml instead of being
// duplicated once per host (which would make generated input quadratic).
const manifestPrivateHostsGroupVarsThreshold = 32

// cmdManifestAnsible materialises the Ansible inventory shape from the same
// manifest that drives the on-host renderer. The generated inventory is
// intentionally separate from deploy/ansible/inventory/ so a fleet can use
// a manifest-specific directory without editing committed IPs or host_vars.
// It emits only the production split-box groups; the retired one-box Ansible
// path is deliberately not representable here.
func cmdManifestAnsible(args []string) int {
	fs := flag.NewFlagSet("manifest ansible", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	manifestFile := fs.String("manifest-file", "", "path to the manifest YAML file (required)")
	outputDir := fs.String("output-dir", "", "generated Ansible root (required)")
	force := fs.Bool("force", false, "replace differing generated files")
	dryRun := fs.Bool("dry-run", false, "print planned files without writing")
	jsonOut := fs.Bool("json", false, "emit structured JSON to stdout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *manifestFile == "" || *outputDir == "" {
		fmt.Fprintln(os.Stderr, "gregalectl manifest ansible: --manifest-file and --output-dir are required")
		return 2
	}

	m, err := manifest.Load(*manifestFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl manifest ansible: load: %v\n", err)
		return 3
	}
	if errs := m.Validate(); errs != nil {
		fmt.Fprintf(os.Stderr, "gregalectl manifest ansible: invalid manifest: %s\n", errs)
		return 1
	}
	files, err := renderManifestAnsibleFiles(m, *outputDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl manifest ansible: %v\n", err)
		return 1
	}

	written := make([]string, 0, len(files))
	for _, file := range files {
		if *dryRun {
			written = append(written, file.Path)
			continue
		}
		if err := writeGeneratedAnsibleFile(file.Path, file.Body, *force); err != nil {
			fmt.Fprintf(os.Stderr, "gregalectl manifest ansible: %v\n", err)
			return 1
		}
		written = append(written, file.Path)
	}

	if *jsonOut || jsonOutput {
		jsonEmit(os.Stdout, struct {
			Manifest string   `json:"manifest"`
			Output   string   `json:"output_dir"`
			Files    []string `json:"files"`
			DryRun   bool     `json:"dry_run"`
		}{*manifestFile, *outputDir, written, *dryRun})
		return 0
	}
	for _, path := range written {
		_, _ = fmt.Fprintln(os.Stdout, path)
	}
	return 0
}

func renderManifestAnsibleFiles(m *manifest.Manifest, outputDir string) ([]manifestAnsibleFile, error) {
	if !filepath.IsAbs(outputDir) {
		return nil, fmt.Errorf("output-dir must be absolute to avoid writing inventory relative to an unexpected working directory")
	}
	if len(m.Fleet.Hosts) == 0 {
		return nil, fmt.Errorf("manifest declares no hosts")
	}
	for _, host := range m.Fleet.Hosts {
		if host.Role != roleControlPlane && host.Role != roleComputeOnly {
			return nil, fmt.Errorf("host %s has unsupported production role %q; use control-plane or compute-only", host.Name, host.Role)
		}
	}
	internalHosts, err := renderManifestInternalHosts(m)
	if err != nil {
		return nil, err
	}

	var controlPlane, computeOnly []string
	var hostVars []manifestAnsibleFile
	var postgresListenAddress string
	var postgresAllowedCIDRs []string
	var computeAllowedCIDRs []string
	var controlPlaneAllowedCIDRs []string
	var gatewaySynthTarget string
	var scheddTarget string
	var controlPlaneAPIDLoopback string
	sharedPrivateHosts := len(m.Fleet.Hosts) > manifestPrivateHostsGroupVarsThreshold
	for _, fleetHost := range m.Fleet.Hosts {
		if fleetHost.Role == roleControlPlane {
			scheddTarget, err = manifest.ServiceTCPURL(fleetHost.Role, fleetHost.Address)
			if err != nil {
				return nil, fmt.Errorf("host %s scheduler address: %w", fleetHost.Name, err)
			}
			address, _, parseErr := manifest.ParseHostPort(fleetHost.Address)
			if parseErr != nil {
				return nil, fmt.Errorf("host %s postgres address: %w", fleetHost.Name, parseErr)
			}
			controlPlaneAPIDLoopback = "http://" + net.JoinHostPort(address, "8081")
			postgresListenAddress = address
			controlPlaneAllowedCIDRs = appendHostCIDR(controlPlaneAllowedCIDRs, address, m.Overlay.CIDR)
		}
		if fleetHost.Role == roleComputeOnly {
			address, _, parseErr := manifest.ParseHostPort(fleetHost.Address)
			if parseErr != nil {
				return nil, fmt.Errorf("host %s postgres allow address: %w", fleetHost.Name, parseErr)
			}
			postgresAllowedCIDRs = appendHostCIDR(postgresAllowedCIDRs, address, m.Overlay.CIDR)
			computeAllowedCIDRs = appendHostCIDR(computeAllowedCIDRs, address, m.Overlay.CIDR)
			if gatewaySynthTarget == "" {
				// Schedd runs on the control plane. It targets the local
				// public gateway, which then uses the same DB-backed compute
				// pool as external traffic; no scheduler config names a
				// particular compute node.
				gatewaySynthTarget = "tcp://127.0.0.1:8080"
			}
		}
	}
	for _, host := range m.Fleet.Hosts {
		targetURL := ""
		ansibleHost, _, parseErr := manifest.ParseHostPort(host.Address)
		if parseErr != nil {
			return nil, fmt.Errorf("host %s: %w", host.Name, parseErr)
		}
		if host.Role == "compute-only" {
			targetURL, parseErr = manifest.TCPURL(host.Address)
			if parseErr != nil {
				return nil, fmt.Errorf("host %s target: %w", host.Name, parseErr)
			}
		}
		switch host.Role {
		case roleControlPlane:
			controlPlane = append(controlPlane, host.Name)
		case roleComputeOnly:
			computeOnly = append(computeOnly, host.Name)
		default:
			return nil, fmt.Errorf("host %s has unsupported production role %q; use control-plane or compute-only", host.Name, host.Role)
		}
		overlayCIDRs := ""
		if host.Role == roleComputeOnly {
			// The manifest's overlay CIDR is the fleet-wide network contract.
			// Only compute boxes route tenant traffic through that overlay;
			// the control plane keeps the canonical empty list.
			overlayCIDRs = m.Overlay.CIDR
		}
		privateHosts := internalHosts
		if sharedPrivateHosts {
			privateHosts = nil
		}
		hostScheddTarget, targetErr := manifestScheddTarget(m, host, scheddTarget)
		if targetErr != nil {
			return nil, fmt.Errorf("host %s schedd target: %w", host.Name, targetErr)
		}
		body := renderManifestHostVars(host, ansibleHost, targetURL, gatewaySynthTarget, hostScheddTarget, controlPlaneAPIDLoopback, privateHosts, overlayCIDRs, m.Overlay.Provider, m.PrivateDNS.Mode, m.PrivateDNS.Zone, postgresListenAddress, postgresAllowedCIDRs, computeAllowedCIDRs, controlPlaneAllowedCIDRs, m.Storage.FastRoot)
		hostVars = append(hostVars, manifestAnsibleFile{
			Path: filepath.Join(outputDir, "inventory", "host_vars", host.Name+".yml"),
			Body: []byte(body),
		})
	}

	var inventory bytes.Buffer
	inventory.WriteString("# Generated by gregalectl manifest ansible; do not hand-edit.\n")
	writeInventoryGroup(&inventory, "control_plane", controlPlane)
	writeInventoryGroup(&inventory, "compute_nodes", computeOnly)
	files := []manifestAnsibleFile{{
		Path: filepath.Join(outputDir, "inventory", "hosts.ini"),
		Body: inventory.Bytes(),
	}}
	if sharedPrivateHosts {
		files = append(files, manifestAnsibleFile{
			Path: filepath.Join(outputDir, "inventory", "group_vars", "all.yml"),
			Body: []byte(renderManifestPrivateResolverVars(internalHosts, m.PrivateDNS.Mode, m.PrivateDNS.Zone)),
		})
	}
	files = append(files, hostVars...)
	return files, nil
}

// manifestScheddTarget returns the endpoint a host-local daemon should use.
// The control-plane scheduler keeps its stable role identity; a compute host
// owns a schedd beside vmmd, so its vmmd and gatewayd clients must dial that
// host directly. Fleet host addresses describe vmmd (50051), while the schedd
// bind port comes from the manifest's schedd daemon block.
func manifestScheddTarget(m *manifest.Manifest, host manifest.Host, controlPlaneTarget string) (string, error) {
	if host.Role != roleComputeOnly {
		return controlPlaneTarget, nil
	}
	if m.Daemons.Schedd == nil {
		return "", fmt.Errorf("manifest.daemons.schedd is required for compute-only hosts")
	}
	bind := strings.TrimPrefix(m.Daemons.Schedd.Bind, "tcp://")
	if bind == m.Daemons.Schedd.Bind {
		return "", fmt.Errorf("schedd bind %q must use tcp:// for compute-only hosts", m.Daemons.Schedd.Bind)
	}
	_, portText, err := net.SplitHostPort(bind)
	if err != nil {
		return "", fmt.Errorf("schedd bind %q: %w", m.Daemons.Schedd.Bind, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("schedd bind %q has invalid port", m.Daemons.Schedd.Bind)
	}
	address, _, err := manifest.ParseHostPort(host.Address)
	if err != nil {
		return "", err
	}
	return "tcp://" + net.JoinHostPort(address, strconv.Itoa(port)), nil
}

// appendHostCIDR turns a fleet endpoint into the narrowest firewall source
// range available. Literal endpoints retain the /32 behavior used by the
// original generator. Hostname endpoints cannot be resolved safely while
// rendering, so the manifest's declared overlay CIDR becomes the explicit
// private-network boundary; this keeps hostname-based fleets functional
// without copying an address into generated Ansible files.
func appendHostCIDR(values []string, address, overlayCIDR string) []string {
	value := overlayCIDR
	if _, err := netip.ParseAddr(address); err == nil {
		value = address + "/32"
	}
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func renderManifestInternalHosts(m *manifest.Manifest) ([]manifestInternalHost, error) {
	internalHosts := make([]manifestInternalHost, 0, len(m.Fleet.Hosts))
	firstCompute := ""
	for _, host := range m.Fleet.Hosts {
		if host.Role == roleComputeOnly {
			if firstCompute == "" {
				firstCompute = host.Name
			}
		}
	}
	for _, host := range m.Fleet.Hosts {
		serviceName, err := manifest.ServiceName(host.Role)
		if err != nil {
			return nil, fmt.Errorf("host %s: internal service identity: %w", host.Name, err)
		}
		address, _, err := manifest.ParseHostPort(host.Address)
		if err != nil {
			return nil, fmt.Errorf("host %s: internal service address: %w", host.Name, err)
		}
		entry := manifestInternalHost{Names: []string{serviceName}}
		if _, err := netip.ParseAddr(address); err == nil {
			// Literal endpoint manifests retain their explicit address for
			// backwards compatibility with local fixtures. Keep the
			// manifest host name as a stable local alias so every entry
			// remains usable even when the endpoint is an IP literal.
			entry.Address = address
			entry.Names = append([]string{host.Name}, entry.Names...)
		} else {
			// Hostname manifests are resolved from inventory host facts by
			// the Ansible adapter. This keeps IPs out of generated config
			// and avoids depending on a public DNS provider.
			entry.InventoryHost = host.Name
			entry.Names = append([]string{address}, entry.Names...)
		}
		if host.Role == roleComputeOnly {
			// Stable hostnames are the node-specific routing records.
			// Keep the role aliases only on the first compute host as
			// compatibility fallbacks for static schedd/meterd config;
			// emitting vmmd.faas or egress.faas on every compute host
			// would make /etc/hosts resolution ambiguous.
			if host.Name == firstCompute {
				entry.Names = append(entry.Names, "egress.faas")
			} else {
				entry.Names = filterInternalHostNames(entry.Names, "vmmd.faas")
			}
		}
		if host.Role == roleControlPlane {
			// apid.faas is a stable private service identity. It resolves to
			// the control-plane address from this manifest's resolver map and
			// is independent of the provider or the host's public IP.
			entry.Names = append(entry.Names, "apid.faas")
		}
		internalHosts = append(internalHosts, entry)
	}
	return internalHosts, nil
}

func filterInternalHostNames(names []string, omit ...string) []string {
	blocked := make(map[string]struct{}, len(omit))
	for _, name := range omit {
		blocked[name] = struct{}{}
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		if _, ok := blocked[name]; !ok {
			out = append(out, name)
		}
	}
	return out
}

func writeInventoryGroup(out *bytes.Buffer, group string, hosts []string) {
	out.WriteString("[")
	out.WriteString(group)
	out.WriteString("]\n")
	for _, host := range hosts {
		out.WriteString(host)
		out.WriteByte('\n')
	}
	out.WriteByte('\n')
}

func renderManifestHostVars(host manifest.Host, ansibleHost, targetURL, gatewaySynthTarget, scheddTarget, controlPlaneAPIDLoopback string, internalHosts []manifestInternalHost, overlayCIDRs, overlayProvider, privateDNSMode, privateDNSZone, postgresListenAddress string, postgresAllowedCIDRs, computeAllowedCIDRs, controlPlaneAllowedCIDRs []string, storageMountpoint string) string {
	var b strings.Builder
	canonicalNodeName := canonicalComputeNodeName(host.Name, roleTemplating.Role(host.Role))
	fmt.Fprintf(&b, "# Generated from the split-box manifest for %s; do not hand-edit.\n", host.Name)
	fmt.Fprintf(&b, "faas_box_role: %s\n", host.Role)
	fmt.Fprintf(&b, "faas_node_name: %s\n", canonicalNodeName)
	fmt.Fprintf(&b, "ansible_host: %q\n", ansibleHost)
	b.WriteString("ansible_python_interpreter: /usr/bin/python3\n")
	if host.StorageDevice != "" {
		fmt.Fprintf(&b, "faas_storage_device: %q\n", host.StorageDevice)
	}
	if storageMountpoint != "" {
		fmt.Fprintf(&b, "faas_storage_mountpoint: %q\n", storageMountpoint)
	}
	fmt.Fprintf(&b, "faas_overlay_provider: %q\n", overlayProvider)
	switch overlayProvider {
	case "tailscale":
		b.WriteString("faas_overlay_iface: tailscale0\n")
	case "wireguard":
		b.WriteString("faas_overlay_iface: wg0\n")
	case "static":
		b.WriteString("faas_overlay_iface: \"{{ ansible_default_ipv4.interface | default('eth0') }}\"\n")
	}
	if host.Role == roleComputeOnly && overlayCIDRs != "" {
		fmt.Fprintf(&b, "overlay_cidrs: [%q]\n", overlayCIDRs)
	} else {
		b.WriteString("overlay_cidrs: []\n")
	}
	if privateDNSMode != "" {
		fmt.Fprintf(&b, "faas_private_dns_mode: %q\n", privateDNSMode)
		fmt.Fprintf(&b, "faas_private_dns_zone: %q\n", privateDNSZone)
	}
	if len(internalHosts) > 0 {
		b.WriteString("faas_private_hosts:\n")
		for _, internalHost := range internalHosts {
			b.WriteString("  - ")
			if internalHost.Address != "" {
				fmt.Fprintf(&b, "address: %q\n", internalHost.Address)
			} else {
				fmt.Fprintf(&b, "inventory_host: %q\n", internalHost.InventoryHost)
			}
			fmt.Fprintf(&b, "    names: [%s]\n", quotedYAMLList(internalHost.Names))
		}
	}
	if host.Role == roleComputeOnly {
		b.WriteString("faas_vmmd_listen_addr: \"tcp://0.0.0.0:50051\"\n")
		fmt.Fprintf(&b, "faas_vmmd_target_url: %q\n", targetURL)
		computeAddress, _, _ := manifest.ParseHostPort(host.Address)
		fmt.Fprintf(&b, "faas_gateway_target_url: %q\n", "tcp://"+net.JoinHostPort(computeAddress, "8080"))
		fmt.Fprintf(&b, "faas_vmmd_schedd_target: %q\n", scheddTarget)
		fmt.Fprintf(&b, "faas_gatewayd_schedd_target: %q\n", scheddTarget)
		fmt.Fprintf(&b, "faas_gatewayd_apid_loopback: %q\n", controlPlaneAPIDLoopback)
		fmt.Fprintf(&b, "faas_gatewayd_egress_listen: %q\n", fmt.Sprintf("tcp://0.0.0.0:%d", manifestGatewayEgressPort))
		fmt.Fprintf(&b, "faas_gatewayd_app_errors_target: %q\n", fmt.Sprintf("tcp://apid.faas:%d", manifestAppErrorsPort))
		b.WriteString("faas_gateway_listen: \"0.0.0.0:8080\"\n")
		// Multi-host safety cluster PR-9 (audit F8-B): emit
		// faas_public_listen_addr so the ansible role passes
		// FAAS_PUBLIC_LISTEN_ADDR=... to gatewayd-public and
		// the PR-8 boot-time check (requirePublicBindInMultiHost)
		// does not fire. Without this emission, a correctly
		// bootstrapped fleet reaches the boot-time error and
		// refuses to start; with it, the operator only sees
		// the loopback default on a single-box dev install.
		fmt.Fprintf(&b, "faas_public_listen_addr: %q\n", renderPublicListenAddr(host))
		fmt.Fprintf(&b, "faas_public_control_addr: %q\n", renderPublicControlAddr(host))
	}
	if host.Role == roleControlPlane {
		// Public ingress discovers active compute gateways from the database.
		// This keeps the control-plane API healthy while every compute node is
		// drained and makes node add/drain a data change, not a systemd rewrite.
		b.WriteString("faas_compute_gateway_discovery: database\n")
		fmt.Fprintf(&b, "faas_apid_app_errors_listen: %q\n", fmt.Sprintf("tcp://0.0.0.0:%d", manifestAppErrorsPort))
	}
	if host.Role == roleControlPlane && gatewaySynthTarget != "" {
		b.WriteString("faas_meterd_config_managed: true\n")
		fmt.Fprintf(&b, "faas_meterd_schedd_socket: %q\n", scheddTarget)
		fmt.Fprintf(&b, "faas_meterd_egress_socket: %q\n", fmt.Sprintf("tcp://egress.faas:%d", manifestGatewayEgressPort))
		b.WriteString("faas_meterd_schedd_tls_cert_path: /etc/faas/tls/meterd/schedd-client.crt\n")
		b.WriteString("faas_meterd_schedd_tls_key_path: /etc/faas/tls/meterd/schedd-client.key\n")
		b.WriteString("faas_meterd_schedd_tls_ca_path: /etc/faas/tls/ca/ca.crt\n")
		fmt.Fprintf(&b, "faas_schedd_gateway_synth_target: %q\n", gatewaySynthTarget)
		// gatewayd-internal deliberately binds its control/metrics
		// listener to loopback. Do not generate an unreachable remote
		// scrape URL; the explicit empty override disables schedd's
		// optional scale-up signal until a dedicated metrics relay is
		// provisioned.
		b.WriteString("faas_schedd_gateway_metrics_url: \"\"\n")
	}
	if host.Role == roleControlPlane && postgresListenAddress != "" {
		fmt.Fprintf(&b, "faas_postgres_listen_addresses: %q\n", postgresListenAddress)
		if len(postgresAllowedCIDRs) == 0 {
			b.WriteString("faas_postgres_allowed_cidrs: []\n")
		} else {
			fmt.Fprintf(&b, "faas_postgres_allowed_cidrs: [%s]\n", quotedYAMLList(postgresAllowedCIDRs))
		}
		if len(computeAllowedCIDRs) == 0 {
			b.WriteString("faas_compute_allowed_cidrs: []\n")
		} else {
			fmt.Fprintf(&b, "faas_compute_allowed_cidrs: [%s]\n", quotedYAMLList(computeAllowedCIDRs))
		}
	}
	if host.Role == roleComputeOnly {
		if len(controlPlaneAllowedCIDRs) == 0 {
			b.WriteString("faas_control_plane_allowed_cidrs: []\n")
		} else {
			fmt.Fprintf(&b, "faas_control_plane_allowed_cidrs: [%s]\n", quotedYAMLList(controlPlaneAllowedCIDRs))
		}
	}
	return b.String()
}

func renderManifestPrivateResolverVars(internalHosts []manifestInternalHost, privateDNSMode, privateDNSZone string) string {
	var b strings.Builder
	b.WriteString("# Generated from the split-box manifest; do not hand-edit.\n")
	if privateDNSMode != "" {
		fmt.Fprintf(&b, "faas_private_dns_mode: %q\n", privateDNSMode)
		fmt.Fprintf(&b, "faas_private_dns_zone: %q\n", privateDNSZone)
	}
	b.WriteString("faas_private_hosts:\n")
	for _, internalHost := range internalHosts {
		b.WriteString("  - ")
		if internalHost.Address != "" {
			fmt.Fprintf(&b, "address: %q\n", internalHost.Address)
		} else {
			fmt.Fprintf(&b, "inventory_host: %q\n", internalHost.InventoryHost)
		}
		fmt.Fprintf(&b, "    names: [%s]\n", quotedYAMLList(internalHost.Names))
	}
	return b.String()
}

func quotedYAMLList(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = fmt.Sprintf("%q", value)
	}
	return strings.Join(quoted, ", ")
}

// renderPublicListenAddr is the PR-9 emit for
// faas_public_listen_addr. Multi-host safety cluster (audit F8-B):
// the PR-8 boot-time check (gatewayd-public/main.go:625
// requirePublicBindInMultiHost) refuses to start the public
// listener on a loopback default when FAAS_NODE_NAME is set. The
// manifest renderer must emit an explicit host:port from the host
// row so the ansible role passes FAAS_PUBLIC_LISTEN_ADDR and the
// check does not fire.
//
// Single-box posture (the host's Address is loopback) is preserved
// — operators who intentionally want loopback bind keep it. Multi-
// box hosts with a public IP get that IP; multi-box hosts with
// just a private overlay address get 0.0.0.0 + the same port (the
// LB reaches the box via the public path the operator wires
// upstream).
func renderPublicListenAddr(host manifest.Host) string {
	address, port, err := manifest.ParseHostPort(host.Address)
	if err != nil || address == "" {
		return "0.0.0.0:443"
	}
	_ = port
	// Public listen addr: the public-facing host:port. Without a
	// separate PublicIP field, default to the host's address; the
	// loopback case is preserved (single-box posture).
	return net.JoinHostPort(address, "443")
}

// renderPublicControlAddr is the companion emit for
// faas_public_control_addr. Mirrors renderPublicListenAddr shape
// but pins to the canonical :9092 control listener port.
func renderPublicControlAddr(host manifest.Host) string {
	address, _, err := manifest.ParseHostPort(host.Address)
	if err != nil || address == "" {
		return "0.0.0.0:9092"
	}
	return net.JoinHostPort(address, "9092")
}

func writeGeneratedAnsibleFile(path string, body []byte, force bool) error {
	if existing, err := os.ReadFile(path); err == nil {
		if bytes.Equal(existing, body) {
			return nil
		}
		if !force {
			return fmt.Errorf("refusing to overwrite differing generated file %s; use --force", path)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	return os.WriteFile(path, body, 0o644)
}
