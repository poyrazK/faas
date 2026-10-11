package httpkit

// Audit-map JSON keys (ADR-089). The literal `"app_id"` etc. also appear in
// pg_notify payloads; promoting them to consts keeps goconst quiet without
// sacrificing readability of audit map literals. Shared by cmd/apid and the
// pkg/apid domains (ADR-977).
const (
	AuditKeyRuleID       = "rule_id"
	AuditKeyAppID        = "app_id"
	AuditKeyMatchHost    = "match_host"
	AuditKeyMatchPath    = "match_path"
	AuditKeyMatchMethods = "match_methods"
	AuditKeyPriority     = "priority"
	AuditKeyEnabled      = "enabled"
	AuditKeyKind         = "kind"
	AuditKeyDeploymentID = "deployment_id"
	AuditKeyBuildID      = "build_id"
	AuditKeyRepo         = "repo"
	AuditKeyRef          = "ref"
	AuditKeySourceBytes  = "source_bytes"
	AuditKeyTrustRoot    = "trust_root"
)
