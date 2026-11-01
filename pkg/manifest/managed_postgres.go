package manifest

import (
	"fmt"
	"time"
)

// Native managed PostgreSQL (ADR-1027). Database nodes are fleet hosts
// with role `database`: vmmd runs database VMs only and pgproxyd is their
// one public listener. Each carries the facts schedd's database scheduler
// places by; managed_postgres holds what the whole fleet shares. The
// Ansible generator derives the database_nodes inventory, schedd's
// [database_scheduler], pgproxyd's cancel peers and the compute nodes' app
// route from them, so no host copies another's address.

// RoleDatabase is the fleet role of a native managed-database node.
const RoleDatabase = "database"

// A database node's lifecycle in schedd's database scheduler.
const (
	// DatabaseNodeActive takes new databases (the default).
	DatabaseNodeActive = "active"
	// DatabaseNodeDraining takes none; with evacuate_draining its
	// databases move off it.
	DatabaseNodeDraining = "draining"
	// DatabaseNodeUnavailable marks a lost node: schedd fails its
	// replicated databases over and parks the rest for a point-in-time
	// restore, and the generated configuration stops converging,
	// scraping and routing to it.
	DatabaseNodeUnavailable = "unavailable"
)

// ManagedPostgres is the fleet's native managed PostgreSQL.
type ManagedPostgres struct {
	// HostSuffix is the DNS suffix after a database id: clients and app
	// guests connect to <database-id>.<host_suffix> (pgproxyd's wildcard
	// certificate names *.<host_suffix>).
	HostSuffix string `yaml:"host_suffix"`
	// SharedArchive: every database node's dbbackupd uploads to the same
	// bucket and prefix, so restores, moves and failovers may use any node.
	SharedArchive bool `yaml:"shared_archive,omitempty"`
	// AppRoute gives app guests on compute nodes the private route to
	// pgproxyd (198.18.0.1:5432 through gatewayd-internal).
	AppRoute bool `yaml:"app_route,omitempty"`
	// EvacuateDraining moves databases off draining nodes, one at a time
	// (needs shared_archive).
	EvacuateDraining bool `yaml:"evacuate_draining,omitempty"`
	// DrillEvery spaces each database node's restore drills, as a Go
	// duration (empty: schedd's default, 24h); "off" turns them off.
	DrillEvery string `yaml:"drill_every,omitempty"`
}

// DrillsOff is DrillEvery's value that turns restore drills off.
const DrillsOff = "off"

// minDrillEvery is the shortest drill spacing: a drill restores a whole
// database on the node.
const minDrillEvery = time.Hour

// DatabaseHost is a database node's place in schedd's database scheduler.
type DatabaseHost struct {
	// AdmissionCeilingMB caps the memory of the node's running database
	// VMs (guest memory plus overhead); keep it below the node's RAM less
	// the host reserve and any spares.
	AdmissionCeilingMB int `yaml:"admission_ceiling_mb"`
	// VolumeCapacityBytes caps the provisioned volumes placed on the node.
	VolumeCapacityBytes int64 `yaml:"volume_capacity_bytes"`
	// BootSlots caps concurrent boots (default: schedd's); roughly the
	// node's physical cores.
	BootSlots int `yaml:"boot_slots,omitempty"`
	// PostgresMajors the node offers; empty means every supported one.
	PostgresMajors []int `yaml:"postgres_majors,omitempty"`
	// Lifecycle is active (default), draining or unavailable.
	Lifecycle string `yaml:"lifecycle,omitempty"`
}

// Serving reports whether the node is not marked lost.
func (d *DatabaseHost) Serving() bool {
	return d != nil && d.Lifecycle != DatabaseNodeUnavailable
}

// DatabaseHosts returns the fleet's database nodes in manifest order.
func (f Fleet) DatabaseHosts() []Host {
	var out []Host
	for _, h := range f.Hosts {
		if h.Role == RoleDatabase {
			out = append(out, h)
		}
	}
	return out
}

func (m *Manifest) validateManagedPostgres() Errors {
	var errs Errors
	databases := m.Fleet.DatabaseHosts()
	for i, h := range m.Fleet.Hosts {
		path := fmt.Sprintf("fleet.hosts[%d].database", i)
		if h.Role != RoleDatabase {
			if h.Database != nil {
				errs = append(errs, Error{path, "is only valid on a database host"})
			}
			continue
		}
		if h.Database == nil {
			errs = append(errs, Error{path, "is required on a database host"})
			continue
		}
		if h.Database.AdmissionCeilingMB <= 0 {
			errs = append(errs, Error{path + ".admission_ceiling_mb", "must be positive"})
		}
		if h.Database.VolumeCapacityBytes <= 0 {
			errs = append(errs, Error{path + ".volume_capacity_bytes", "must be positive"})
		}
		if h.Database.BootSlots < 0 || h.Database.BootSlots > 256 {
			errs = append(errs, Error{path + ".boot_slots", "must be between 0 (schedd's default) and 256"})
		}
		switch h.Database.Lifecycle {
		case "", DatabaseNodeActive, DatabaseNodeDraining, DatabaseNodeUnavailable:
		default:
			errs = append(errs, Error{path + ".lifecycle", fmt.Sprintf("lifecycle %q is not one of active, draining, unavailable", h.Database.Lifecycle)})
		}
		for _, major := range h.Database.PostgresMajors {
			if major < 14 || major > 18 {
				errs = append(errs, Error{path + ".postgres_majors", fmt.Sprintf("major %d is not one of 14-18", major)})
			}
		}
	}
	if len(databases) > 0 && len(m.Fleet.Hosts) == len(databases) {
		errs = append(errs, Error{"fleet.hosts", "database hosts need a control plane"})
	}
	// pgproxyd's cancel keys name their instance in one byte.
	if len(databases) > 255 {
		errs = append(errs, Error{"fleet.hosts", fmt.Sprintf("declares %d database hosts; at most 255 are supported", len(databases))})
	}
	mp := m.ManagedPostgres
	switch {
	case mp == nil && len(databases) > 0:
		errs = append(errs, Error{"managed_postgres", "is required when the fleet declares database hosts"})
	case mp != nil && len(databases) == 0:
		errs = append(errs, Error{"managed_postgres", "needs at least one database host"})
	case mp != nil && !looksLikeHostname(mp.HostSuffix):
		errs = append(errs, Error{"managed_postgres.host_suffix", fmt.Sprintf("host_suffix %q must be a valid hostname (e.g. db.gregale.dev)", mp.HostSuffix)})
	}
	// A move hands the archive over through the shared bucket.
	if mp != nil && mp.EvacuateDraining && !mp.SharedArchive {
		errs = append(errs, Error{"managed_postgres.evacuate_draining", "needs shared_archive: moves hand the archive over through the shared bucket"})
	}
	if mp != nil && mp.DrillEvery != "" && mp.DrillEvery != DrillsOff {
		if every, err := time.ParseDuration(mp.DrillEvery); err != nil || every < minDrillEvery {
			errs = append(errs, Error{"managed_postgres.drill_every", fmt.Sprintf("drill_every %q must be %q or a duration of at least %s (e.g. 24h)", mp.DrillEvery, DrillsOff, minDrillEvery)})
		}
	}
	return errs
}
