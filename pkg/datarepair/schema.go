// Package datarepair implements explicitly enrolled PostgreSQL field repair.
// Full row values stay in the customer's database. Installation is an owner action.
package datarepair

import (
	_ "embed"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

//go:embed schema.sql
var schema string

//go:embed retention.sql
var retentionSchema string

//go:embed retention_contract.sql
var retentionContract string

//go:embed sources.sql
var sourcesSchema string

//go:embed checks.sql
var checksSchema string

//go:embed validation.sql
var validationSchema string

//go:embed replacement.sql
var replacementSchema string

//go:embed resolution.sql
var resolutionSchema string

//go:embed verification.sql
var verificationSchema string

//go:embed history.sql
var historySchema string

//go:embed transaction_capture.sql
var transactionCaptureSchema string

// Schema returns the owner-installed schema with centrally defined bounds.
// It is deliberately not executed by constructors or request handlers.
func Schema() string {
	return schemaBounds().Replace(strings.Replace(schema, "__RETENTION_SCHEMA__", retentionSchema+"\n"+checksSchema+"\n"+resolutionSchema+"\n"+retentionContract+"\n"+sourcesSchema+"\n"+replacementSchema+"\n"+validationSchema+"\n"+verificationSchema+"\n"+historySchema+"\n"+transactionCaptureSchema, 1))
}

// UpgradeSchema upgrades an existing installation without reenrolling its
// business tables or changing captured schema fingerprints. The owner applies it.
func UpgradeSchema() string {
	return schemaBounds().Replace("BEGIN;\n" + retentionSchema + "\n" + checksSchema + "\n" + resolutionSchema + "\n" + retentionContract + "\n" + sourcesSchema + "\n" + replacementSchema + "\n" + validationSchema + "\n" + verificationSchema + "\n" + historySchema + "\n" + transactionCaptureSchema + "\nREVOKE ALL ON ALL TABLES IN SCHEMA gregale_repair FROM PUBLIC;\nREVOKE ALL ON ALL FUNCTIONS IN SCHEMA gregale_repair FROM PUBLIC;\nCOMMIT;\n")
}

// RetentionSchema preserves the original retention-specific upgrade entry point.
func RetentionSchema() string { return UpgradeSchema() }

func schemaBounds() *strings.Replacer {
	return strings.NewReplacer(
		"__MAX_ROWS__", strconv.Itoa(api.DataRepairMaxRows),
		"__CAPTURE_CONTEXT_BYTES__", strconv.Itoa(api.DataRepairCaptureContextMaxBytes),
		"__MAX_BYTES__", strconv.Itoa(api.DataRepairMaxJournalBytes),
		"__MAX_PLAN_BYTES__", strconv.Itoa(api.DataRepairMaxPlanBytes),
		"__PLAN_TTL__", strconv.Itoa(api.DataRepairPlanTTLSeconds),
		"__OBSERVED_ROW_BYTES__", strconv.Itoa(api.DataRepairMaxObservedRowBytes),
		"__OBSERVED_PLAN_BYTES__", strconv.Itoa(api.DataRepairMaxObservedPlanBytes),
		"__SOURCE_MAX_PAGE__", strconv.Itoa(api.DataRepairSourceMaxPageSize),
		"__SOURCE_MAX_FILTER_BYTES__", strconv.Itoa(api.DataRepairSourceMaxFilterBytes),
		"__MAX_CHECKS__", strconv.Itoa(api.DataRepairMaxChecks),
		"__CHECK_NAME_BYTES__", strconv.Itoa(api.DataRepairCheckNameMaxBytes),
		"__CHECK_POLICY_BYTES__", strconv.Itoa(api.DataRepairMaxRequestBytes),
		"__ACTOR_MAX_BYTES__", strconv.Itoa(api.DataRepairActorMaxBytes),
		"__RETENTION_MIN__", strconv.Itoa(api.DataRepairRetentionMinSeconds),
		"__PURGE_MAX_ITEMS__", strconv.Itoa(api.DataRepairPurgeMaxItems),
		"__PURGE_MAX_BYTES__", strconv.Itoa(api.DataRepairPurgeMaxBytes),
		"__PURGE_MAX_PLAN_BYTES__", strconv.Itoa(api.DataRepairPurgeMaxPlanBytes),
		"__PURGE_PLAN_TTL__", strconv.Itoa(api.DataRepairPurgePlanTTLSeconds),
	)
}
