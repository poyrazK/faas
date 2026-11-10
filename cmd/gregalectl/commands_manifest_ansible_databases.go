package main

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/manifest"
)

// Native managed PostgreSQL (ADR-1027) in the generated Ansible tree. The
// manifest's database hosts become the database_nodes group, and every
// role that reaches them gets their addresses from the same manifest:
//   - the control plane: schedd's [database_scheduler] and pgproxyd's
//     access to the verifier table;
//   - each database node: its vmmd listener, pgproxyd's schedd target and
//     its cancel instance id and peers;
//   - compute nodes (managed_postgres.app_route): the app route to every
//     pgproxyd;
//   - monitoring: control-plane Prometheus scrapes each database node's
//     node_exporter, vmmd, pgproxyd and dbbackupd on its overlay address,
//     which the node's firewall opens to the control plane only.
//
// A node marked lifecycle: unavailable (lost) stays in schedd's scheduler,
// which fails over or restores its databases, but leaves the inventory,
// the scrape targets, the cancel peers and the app route.

// manifestDatabasePort is pgproxyd's listener on every database node.
const manifestDatabasePort = 5432

// manifestControlPlanePostgresPort is the control plane's PostgreSQL.
const manifestControlPlanePostgresPort = 5432

// manifestDatabaseRoutePort is gatewayd-internal's app-route forwarder,
// bound beside its service proxy on the tenant bridge.
const manifestDatabaseRoutePort = 10083

type manifestDatabase struct {
	host     manifest.Host
	address  string
	vmmdPort int
	// instanceID tags pgproxyd's cancel keys: 1.. in manifest order.
	instanceID int
}

type manifestDatabases struct {
	config      *manifest.ManagedPostgres
	nodes       []manifestDatabase
	overlayCIDR string
	// controlPlane is the control plane's address, where pgproxyd reads
	// role verifiers from PostgreSQL as faas_pgproxyd; scheddPort is its
	// schedd listener, which pgproxyd wakes databases through.
	controlPlane string
	scheddPort   int
}

func renderManifestDatabases(m *manifest.Manifest) (manifestDatabases, error) {
	out := manifestDatabases{config: m.ManagedPostgres, overlayCIDR: m.Overlay.CIDR}
	for _, h := range m.Fleet.Hosts {
		if h.Role == roleControlPlane && out.controlPlane == "" {
			address, port, err := manifest.ParseHostPort(h.Address)
			if err != nil {
				return manifestDatabases{}, fmt.Errorf("control plane %s: %w", h.Name, err)
			}
			out.controlPlane, out.scheddPort = address, port
		}
	}
	for i, h := range m.Fleet.DatabaseHosts() {
		address, port, err := manifest.ParseHostPort(h.Address)
		if err != nil {
			return manifestDatabases{}, fmt.Errorf("database host %s: %w", h.Name, err)
		}
		if h.Database == nil || m.ManagedPostgres == nil {
			return manifestDatabases{}, fmt.Errorf("database host %s needs its database block and managed_postgres", h.Name)
		}
		out.nodes = append(out.nodes, manifestDatabase{host: h, address: address, vmmdPort: port, instanceID: i + 1})
	}
	return out, nil
}

// hostVars is what host needs to reach the database nodes, or to be one.
func (d manifestDatabases) hostVars(host manifest.Host, scheddTarget string) string {
	if len(d.nodes) == 0 {
		return ""
	}
	switch host.Role {
	case roleControlPlane:
		return d.controlPlaneVars()
	case roleComputeOnly:
		if d.config.AppRoute {
			return d.computeVars()
		}
	case manifest.RoleDatabase:
		return d.nodeVars(host, scheddTarget)
	}
	return ""
}

func (d manifestDatabases) controlPlaneVars() string {
	var b strings.Builder
	b.WriteString("# ADR-1027: schedd's database scheduler, from the manifest's database hosts.\n")
	b.WriteString("faas_schedd_database_scheduler:\n")
	fmt.Fprintf(&b, "  shared_archive: %t\n", d.config.SharedArchive)
	if d.config.EvacuateDraining {
		b.WriteString("  evacuate_draining: true\n")
	}
	switch d.config.DrillEvery {
	case "":
	case manifest.DrillsOff:
		// schedd turns drills off for a negative spacing.
		b.WriteString("  drill_every: \"-1s\"\n")
	default:
		fmt.Fprintf(&b, "  drill_every: %q\n", d.config.DrillEvery)
	}
	b.WriteString("  nodes:\n")
	var cidrs []string
	for _, n := range d.nodes {
		db := n.host.Database
		fmt.Fprintf(&b, "    - id: %q\n", n.host.Name)
		fmt.Fprintf(&b, "      target_url: %q\n", "tcp://"+net.JoinHostPort(n.address, strconv.Itoa(n.vmmdPort)))
		fmt.Fprintf(&b, "      admission_ceiling_mb: %d\n", db.AdmissionCeilingMB)
		fmt.Fprintf(&b, "      volume_capacity_bytes: %d\n", db.VolumeCapacityBytes)
		if db.BootSlots > 0 {
			fmt.Fprintf(&b, "      boot_slots: %d\n", db.BootSlots)
		}
		if db.Lifecycle != "" && db.Lifecycle != manifest.DatabaseNodeActive {
			fmt.Fprintf(&b, "      lifecycle: %q\n", db.Lifecycle)
		}
		if len(db.PostgresMajors) > 0 {
			majors := make([]string, len(db.PostgresMajors))
			for i, major := range db.PostgresMajors {
				majors[i] = strconv.Itoa(major)
			}
			fmt.Fprintf(&b, "      postgres_majors: [%s]\n", strings.Join(majors, ", "))
		}
		cidrs = appendHostCIDR(cidrs, n.address, d.overlayCIDR)
	}
	// pgproxyd reads role verifiers as faas_pgproxyd from these sources,
	// and the control plane's firewall admits them to PostgreSQL and
	// schedd only (the compute allow-lists name compute nodes).
	fmt.Fprintf(&b, "faas_postgres_pgproxyd_cidrs: [%s]\n", quotedYAMLList(cidrs))
	fmt.Fprintf(&b, "faas_database_allowed_cidrs: [%s]\n", quotedYAMLList(cidrs))
	fmt.Fprintf(&b, "faas_database_allowed_ports: [%d, %d]\n", manifestControlPlanePostgresPort, d.scheddPort)
	// Database nodes are schedd's, not compute-registry rows, so
	// Prometheus scrapes them from this static list.
	if serving := d.serving(); len(serving) == 0 {
		b.WriteString("prom_database_targets: []\n")
	} else {
		b.WriteString("prom_database_targets:\n")
		for _, n := range serving {
			fmt.Fprintf(&b, "  - node: %q\n    host: %q\n", n.host.Name, urlHost(n.address))
		}
	}
	return b.String()
}

func (d manifestDatabases) nodeVars(host manifest.Host, scheddTarget string) string {
	var self manifestDatabase
	for _, n := range d.nodes {
		if n.host.Name == host.Name {
			self = n
		}
	}
	var b strings.Builder
	b.WriteString("# ADR-1027: this database node, from the manifest.\n")
	fmt.Fprintf(&b, "database_vmmd_overlay_ip: %q\n", self.address)
	fmt.Fprintf(&b, "database_vmmd_port: %d\n", self.vmmdPort)
	if d.overlayCIDR != "" {
		fmt.Fprintf(&b, "database_node_overlay_cidrs: [%q]\n", d.overlayCIDR)
	}
	fmt.Fprintf(&b, "pgproxyd_host_suffix: %q\n", d.config.HostSuffix)
	fmt.Fprintf(&b, "pgproxyd_schedd_target: %q\n", scheddTarget)
	// database_node.yml's first play settles the faas_pgproxyd password on
	// the control plane (generated once, or the Vault value) and creates
	// the role with it; the DSN reads the same fact, so the two cannot drift.
	fmt.Fprintf(&b, "pgproxyd_db_url: \"postgres://faas_pgproxyd:{{ hostvars[groups['control_plane'] | first].faas_pgproxyd_database_password | urlencode | replace('/', '%%2F') }}@%s/faas?sslmode=disable\"\n",
		net.JoinHostPort(d.controlPlane, strconv.Itoa(manifestControlPlanePostgresPort)))
	fmt.Fprintf(&b, "pgproxyd_instance_id: %d\n", self.instanceID)
	fmt.Fprintf(&b, "database_node_metrics_host: %q\n", urlHost(self.address))
	fmt.Fprintf(&b, "database_node_metrics_cidrs: [%s]\n", quotedYAMLList(appendHostCIDR(nil, d.controlPlane, d.overlayCIDR)))
	var peers []manifestDatabase
	for _, n := range d.serving() {
		if n.host.Name != host.Name {
			peers = append(peers, n)
		}
	}
	if len(peers) == 0 {
		b.WriteString("pgproxyd_cancel_peers: {}\n")
		return b.String()
	}
	b.WriteString("pgproxyd_cancel_peers:\n")
	for _, n := range peers {
		fmt.Fprintf(&b, "  %d: %q\n", n.instanceID, net.JoinHostPort(n.address, strconv.Itoa(manifestDatabasePort)))
	}
	return b.String()
}

// urlHost is address as the host part of host:port.
func urlHost(address string) string {
	if strings.Contains(address, ":") {
		return "[" + address + "]"
	}
	return address
}

// serving is the nodes not marked lost, in manifest order. Instance ids
// stay those of the full list, so marking a node lost renumbers nothing.
func (d manifestDatabases) serving() []manifestDatabase {
	var out []manifestDatabase
	for _, n := range d.nodes {
		if n.host.Database.Serving() {
			out = append(out, n)
		}
	}
	return out
}

func (d manifestDatabases) computeVars() string {
	nodes := d.serving()
	if len(nodes) == 0 {
		// gatewayd-internal refuses a route without upstreams.
		nodes = d.nodes
	}
	upstreams := make([]string, len(nodes))
	for i, n := range nodes {
		upstreams[i] = net.JoinHostPort(n.address, strconv.Itoa(manifestDatabasePort))
	}
	var b strings.Builder
	b.WriteString("# ADR-1027: the app route from guests to pgproxyd, from the manifest.\n")
	b.WriteString("faas_database_route_enabled: true\n")
	fmt.Fprintf(&b, "gatewayd_database_route_listen: \"{{ gatewayd_service_proxy_listen | regex_replace(':[0-9]+$', ':%d') }}\"\n", manifestDatabaseRoutePort)
	fmt.Fprintf(&b, "gatewayd_database_route_upstreams: [%s]\n", quotedYAMLList(upstreams))
	fmt.Fprintf(&b, "gatewayd_database_route_host_suffix: %q\n", d.config.HostSuffix)
	return b.String()
}
