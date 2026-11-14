package datapipeline

import (
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const neonReplicationDocs = "https://neon.com/docs/guides/logical-replication-neon"

type ProviderGuide struct {
	Provider      string   `json:"provider"`
	Documentation string   `json:"documentation"`
	Qualification string   `json:"qualification"`
	Steps         []string `json:"steps"`
}

// NeonSourceGuide is read-only guidance, not a compatibility or capacity grant.
func NeonSourceGuide() ProviderGuide {
	return ProviderGuide{Provider: "neon", Documentation: neonReplicationDocs, Qualification: "live_provider_evidence_required", Steps: []string{
		"Use the direct PostgreSQL 16 connection, without -pooler in its hostname, and sslmode=verify-full.",
		"Enable logical replication in Neon project settings. This restarts the project's computes and cannot be reverted.",
		"Use a dedicated role with REPLICATION, database CREATE, SELECT and ownership rights for the selected tables. Verify actual role privileges; do not assume a SQL-created role has them.",
		"If Neon IP Allow is enabled, admit both the Gregale worker and the managed subscriber's qualified egress addresses.",
		"Verify a positive max_slot_wal_keep_size no greater than 1GB. If unavailable, request operator/provider review. Neon's inactive-slot expiry is not a byte-bounded retention guarantee.",
		"A connected subscriber keeps source compute active. Review continuous compute usage before starting replication.",
		"Inactive slots may be removed after approximately 40 hours, and branch restore removes slots. Treat a missing slot as requiring confirmed recovery.",
		"Run the fresh source/mirror checks and the complete isolated real-provider qualification drill before a customer pilot.",
	}}
}

func neonSource(raw string) (bool, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return false, false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if !strings.HasSuffix(host, ".neon.tech") {
		return false, false
	}
	endpoint, _, _ := strings.Cut(host, ".")
	return true, strings.HasSuffix(endpoint, "-pooler")
}

func neonCheckGuidance(result *api.DataConnectionCheck, raw string) {
	if neon, _ := neonSource(raw); !neon {
		return
	}
	result.SourceProviderHint, result.SourceGuidanceURL = "neon", neonReplicationDocs
	for i := range result.Checks {
		c := &result.Checks[i]
		if c.State != "blocked" {
			continue
		}
		switch c.Code {
		case "source_logical_replication_required":
			c.Remediation = "Enable logical replication in Neon project settings; this restarts computes. Review the Neon connection guide before proceeding."
		case "source_replication_permission_required":
			c.Remediation = "Verify the dedicated Neon role's REPLICATION privilege using the supported Console/API role setup. SQL-created roles may lack it."
		case "source_wal_retention_limit_required":
			c.Remediation = "Ask the operator/provider for a positive native WAL retention cap no greater than 1GB. Neon inactive-slot expiry does not satisfy this limit; admission remains blocked."
		}
	}
}
