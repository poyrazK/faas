package api

import "testing"

func TestOperationPlanBoundsSupportRetentionAndReporting(t *testing.T) {
	for _, plan := range []Plan{PlanFree, PlanHobby, PlanPro, PlanScale} {
		t.Run(string(plan), func(t *testing.T) {
			limits := MustLimitsFor(plan)
			op := limits.Operations
			if !op.Allowed {
				if plan != PlanFree {
					t.Fatal("paid plan has no operation policy")
				}
				return
			}
			if plan == PlanFree {
				t.Fatal("Free must not admit operations")
			}
			if op.IdempotencyRetentionSeconds <= op.ResultRetentionSeconds || op.EventRetentionSeconds > op.ResultRetentionSeconds || op.EventRetentionSeconds <= 0 {
				t.Fatal("submission tombstones must outlive results; events need a bounded result horizon")
			}
			if op.ArtifactMaxBytes <= 0 || op.ArtifactMaxBytes > op.ArtifactTotalMaxBytes || op.ArtifactTotalMaxBytes > OperationArtifactSpoolMaxBytes {
				t.Fatal("artifact policy exceeds verification capacity or cannot hold a single artifact")
			}
			if op.RetainedArtifactBytesPerAccount < op.ArtifactTotalMaxBytes || op.RetainedArtifactsPerAccount < op.ArtifactsPerOperation {
				t.Fatal("account storage bounds must hold one complete operation result")
			}
			if op.ReportsPerOperation < op.ProgressStages+op.ArtifactsPerOperation || op.ReportBytes > OperationReportBodyMaxBytes || op.ReportMinIntervalMS <= 0 {
				t.Fatal("report policy must support the declared stages and attachments within protocol bounds")
			}
			if op.DefinitionsPerApp <= 0 || op.PendingPerAccount <= 0 || op.RecoveriesPerOperation <= 0 || op.SubscriptionsPerAccount <= 0 || op.SchemaBytes <= 0 || limits.MaxSourceBytesPerInvocation > OperationSubmissionMaxBytes {
				t.Fatal("allowed operations need bounded admission, schema, recovery and stream capacity")
			}
		})
	}
	if OperationExecutionRenewTimeout >= OperationExecutionLeaseMax || OperationStreamAuthInterval >= OperationStreamLease || OperationStreamRenewInterval >= OperationStreamLease {
		t.Fatal("execution and stream leases must leave time for bounded renewal and authorization checks")
	}
	if OperationArtifactStagingLifetime <= OperationArtifactTransferTimeout || OperationArtifactCleanupLease <= OperationArtifactTransferTimeout+OperationExecutionRenewTimeout {
		t.Fatal("storage intents and cleanup claims must outlast their bounded transfers")
	}
}
