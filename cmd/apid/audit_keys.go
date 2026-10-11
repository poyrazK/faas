package main

import "github.com/onebox-faas/faas/pkg/apid/httpkit"

// Audit-map JSON keys; see httpkit.AuditKey* (ADR-089, ADR-977).
const (
	auditKeyRuleID       = httpkit.AuditKeyRuleID
	auditKeyAppID        = httpkit.AuditKeyAppID
	auditKeyMatchHost    = httpkit.AuditKeyMatchHost
	auditKeyMatchPath    = httpkit.AuditKeyMatchPath
	auditKeyMatchMethods = httpkit.AuditKeyMatchMethods
	auditKeyPriority     = httpkit.AuditKeyPriority
	auditKeyEnabled      = httpkit.AuditKeyEnabled
	auditKeyKind         = httpkit.AuditKeyKind
	auditKeyDeploymentID = httpkit.AuditKeyDeploymentID
	auditKeyBuildID      = httpkit.AuditKeyBuildID
	auditKeyRepo         = httpkit.AuditKeyRepo
	auditKeyRef          = httpkit.AuditKeyRef
	auditKeySourceBytes  = httpkit.AuditKeySourceBytes
	auditKeyTrustRoot    = httpkit.AuditKeyTrustRoot
)
