package deploydiff

import (
	"github.com/onebox-faas/faas/pkg/api"
)

// QuotaConfig carries the plan-tier limits the gate enforces.
// Pulled from [api.MustLimitsFor] once at the call site; never
// inline any limit literal here — per CLAUDE.md "Limits in one
// place" + "never inline a limit at its point of use", every
// limit below is read from the [api.Limits] struct the caller
// hands in.
//
// The gate runs as a separate pass over [Pending] + [Baseline] —
// not interleaved with the diff engine's [Compute] — so the gate's
// order is stable (every quota break is a "would ship" check, not
// an "is currently shipped" check). It also means the gate can be
// unit-tested in isolation from [Compute].
type QuotaConfig struct {
	Limits api.Limits
	// AccountCronCount is the current per-account cron count
	// (across every app). The wire surface is GET /v1/crons; the
	// CLI captures it before running the diff. 0 when unknown.
	AccountCronCount int
	// AccountEdgeRuleCount is the per-account edge rule count.
	// 0 when unknown.
	AccountEdgeRuleCount int
}

// Quota returns the list of plan-tier breaks. Empty slice means
// no quota violation. Each Break.Code matches an [api.Code*]
// constant so the CLI error renders identically to what a real
// deploy would say.
//
// The gate fires on:
//   - Plan cap breaches (RAM_MB, MAX_CONCURRENCY)
//   - Per-app cron cap (would push the app over its cap)
//   - Per-account cron cap (would push the account over its cap)
//   - Per-app edge rule cap
//   - Per-app env cap
//   - Per-secret / per-env value byte cap
//   - Plan feature gates (Free → cron / edge rule JWT / etc.)
//   - Egress allowlist size cap
//
// The gate does NOT fire on:
//   - Immutable deployment fields (those are [Break] from the
//     diff engine, severity "warn", not a quota break)
//   - Schema breaks (diff engine emits those)
//   - Anything that the apid server's CreateApp / CreateCron /
//     CreateEdgeRule / UpdateApp handlers check atomically (the
//     CLI's pre-check is best-effort UX; the server is the
//     authority per [pkg/state.CreateCronIfUnderQuota]'s
//     FOR UPDATE pattern).
func Quota(p api.Plan, baseline Baseline, pending Pending, cfg QuotaConfig) []Break {
	limits := cfg.Limits
	out := []Break{}

	// RAM cap.
	if pending.AppConfig.RAMMB != nil && *pending.AppConfig.RAMMB > limits.RAMMB {
		out = append(out, Break{
			Code:     api.CodePlanLimitRAM,
			Severity: SeverityError,
			Reason:   "ram_mb exceeds plan cap",
			Field:    "memory",
			Observed: AsAny(*pending.AppConfig.RAMMB),
			Limit:    AsAny(limits.RAMMB),
		})
	}
	if pending.AppConfig.CPUMillicores != nil && !api.ValidAppCPUMillicores(*pending.AppConfig.CPUMillicores) {
		out = append(out, Break{
			Code: api.CodeInvalidAppCPU, Severity: SeverityError,
			Reason: "cpu_millicores must be one of 250, 500, or 1000",
			Field:  "cpu_millicores", Observed: AsAny(*pending.AppConfig.CPUMillicores),
		})
	}
	// MaxConcurrency cap.
	if pending.AppConfig.MaxConcurrency != nil && *pending.AppConfig.MaxConcurrency > limits.MaxConcurrency {
		out = append(out, Break{
			Code:     api.CodePlanLimitConcur,
			Severity: SeverityError,
			Reason:   "max_concurrency exceeds plan cap",
			Field:    "concurrency",
			Observed: AsAny(*pending.AppConfig.MaxConcurrency),
			Limit:    AsAny(limits.MaxConcurrency),
		})
	}
	// MinInstances cap (issue #557 / ADR-071). MaxMinInstances is
	// the per-plan value ceiling (Free 0, Hobby 1, Pro 3, Scale 10).
	if pending.AppConfig.MinInstances != nil && *pending.AppConfig.MinInstances > limits.MaxMinInstances {
		out = append(out, Break{
			Code:     api.CodePlanMinInstancesNotAllowed,
			Severity: SeverityError,
			Reason:   "min_instances exceeds plan cap",
			Field:    "min_instances",
			Observed: AsAny(*pending.AppConfig.MinInstances),
			Limit:    AsAny(limits.MaxMinInstances),
		})
	}
	// Plan feature gate: MinInstancesAllowed bool. Free = false;
	// Hobby+ = true. PATCH-true on Free returns 403.
	if pending.AppConfig.MinInstances != nil && *pending.AppConfig.MinInstances > 0 && !limits.MinInstancesAllowed {
		out = append(out, Break{
			Code:     api.CodePlanMinInstancesNotAllowed,
			Severity: SeverityError,
			Reason:   "min_instances is not enabled on this plan",
			Field:    "min_instances",
			Observed: AsAny(*pending.AppConfig.MinInstances),
			Limit:    AsAny(0),
		})
	}
	// Streaming gate.
	if pending.AppConfig.StreamingEnabled != nil && *pending.AppConfig.StreamingEnabled && !limits.StreamingEnabled {
		out = append(out, Break{
			Code:     api.CodePlanStreamingNotAllowed,
			Severity: SeverityError,
			Reason:   "streaming is not enabled on this plan",
			Field:    "streaming_enabled",
		})
	}
	// WebSocket gate.
	if pending.AppConfig.WebSocketEnabled != nil && *pending.AppConfig.WebSocketEnabled && !limits.WebSocketEnabled {
		out = append(out, Break{
			Code:     api.CodePlanWebSocketNotAllowed,
			Severity: SeverityError,
			Reason:   "websocket is not enabled on this plan",
			Field:    "websocket_enabled",
		})
	}
	// Warm-snapshot gate.
	if pending.AppConfig.WarmSnapshotEnabled != nil && *pending.AppConfig.WarmSnapshotEnabled && !limits.WarmSnapshotEnabled {
		out = append(out, Break{
			Code:     api.CodePlanWarmSnapshotNotAllowed,
			Severity: SeverityError,
			Reason:   "warm_snapshot is not enabled on this plan",
			Field:    "warm_snapshot_enabled",
		})
	}
	// Require-authn gate (issue #560).
	if pending.AppConfig.RequireAuthn != nil && *pending.AppConfig.RequireAuthn && !limits.RequireAuthn {
		out = append(out, Break{
			Code:     api.CodePlanRequireAuthnNotAllowed,
			Severity: SeverityError,
			Reason:   "require_authn is not enabled on this plan",
			Field:    "require_authn",
		})
	}
	// App-protocol gate (ADR-124). Free plans cannot adopt
	// app_protocol='grpc'; http1 / http2 are universal and not
	// gated here. Mirrors the per-plan Plan.AppProtocolAllowed
	// accessor used at every other gate in this file (require_authn,
	// public_auth, warm_snapshot, traffic_split, egress_allowlist).
	if pending.AppConfig.AppProtocol != nil &&
		!p.AppProtocolAllowed(api.AppProtocolGRPC) &&
		*pending.AppConfig.AppProtocol == api.AppProtocolGRPC {
		out = append(out, Break{
			Code:     api.CodePlanAppProtocolGrpcNotAllowed,
			Severity: SeverityError,
			Reason:   "app_protocol='grpc' is not enabled on this plan",
			Field:    "app_protocol",
			Observed: AsAny(*pending.AppConfig.AppProtocol),
			Limit:    AsAny("http1|http2 only"),
		})
	}
	// Egress allowlist gate + size cap.
	if pending.AppConfig.EgressAllowlist != nil {
		if !limits.EgressAllowlistAllowed {
			out = append(out, Break{
				Code:     api.CodePlanEgressAllowlistNotAllowed,
				Severity: SeverityError,
				Reason:   "egress allowlist is not enabled on this plan",
				Field:    "egress_allowlist",
			})
		}
		if limits.EgressAllowlistMaxSize > 0 && len(*pending.AppConfig.EgressAllowlist) > limits.EgressAllowlistMaxSize {
			out = append(out, Break{
				Code:     "egress_allowlist_too_long",
				Severity: SeverityError,
				Reason:   "egress_allowlist exceeds plan size cap",
				Field:    "egress_allowlist",
				Observed: AsAny(len(*pending.AppConfig.EgressAllowlist)),
				Limit:    AsAny(limits.EgressAllowlistMaxSize),
			})
		}
	}
	// Autoscale target RPS gate.
	if pending.AppConfig.AutoscaleTargetRPS != nil && *pending.AppConfig.AutoscaleTargetRPS > 0 && !limits.ScaleUpTargetRPSAllowed {
		out = append(out, Break{
			Code:     api.CodePlanScaleUpNotAllowed,
			Severity: SeverityError,
			Reason:   "autoscale_target_rps is not enabled on this plan",
			Field:    "autoscale_target_rps",
		})
	}
	// Autoscale target CPU gate.
	if pending.AppConfig.AutoscaleTargetCP != nil && *pending.AppConfig.AutoscaleTargetCP > 0 && !limits.ScaleUpTargetCPUAllowed {
		out = append(out, Break{
			Code:     api.CodePlanScaleUpNotAllowed,
			Severity: SeverityError,
			Reason:   "autoscale_target_cpu_pct is not enabled on this plan",
			Field:    "autoscale_target_cpu_pct",
		})
	}

	// Crons.
	if pending.Crons != nil {
		// Per-app cron cap. The diff engine's Cron[schedule path]
		// Changes reflect the post-deploy list; the gate needs the
		// net add/remove to compare against the cap. Count add -
		// remove vs baseline count.
		wanted := len(pending.Crons)
		// Free plan = crons disabled entirely (MaxQueueDepth 0,
		// cron limits 0/0). Gate fires before per-app count.
		if limits.CronLimitPerApp == 0 {
			out = append(out, Break{
				Code:     api.CodePlanCronsNotAllowed,
				Severity: SeverityError,
				Reason:   "crons are not enabled on this plan",
				Field:    "crons",
			})
		} else {
			if wanted > limits.CronLimitPerApp {
				out = append(out, Break{
					Code:     api.CodePlanCronQuota,
					Severity: SeverityError,
					Reason:   "cron count exceeds per-app cap",
					Field:    "crons",
					Observed: AsAny(wanted),
					Limit:    AsAny(limits.CronLimitPerApp),
				})
			}
		}
		// Per-account cron cap. cfg.AccountCronCount + (wanted -
		// existing baseline count for this app) is the post-deploy
		// per-account total.
		existingThisApp := len(baseline.Crons)
		postDeployAcct := cfg.AccountCronCount + (wanted - existingThisApp)
		if postDeployAcct > limits.CronLimitPerAccount {
			out = append(out, Break{
				Code:     api.CodePlanCronQuota,
				Severity: SeverityError,
				Reason:   "cron count exceeds per-account cap",
				Field:    "crons",
				Observed: AsAny(postDeployAcct),
				Limit:    AsAny(limits.CronLimitPerAccount),
			})
		}
	}

	// Edge rules.
	if pending.EdgeRules != nil {
		wanted := len(pending.EdgeRules)
		if wanted > limits.EdgeRulesPerApp {
			out = append(out, Break{
				Code:     api.CodePlanLimitEdgeRules,
				Severity: SeverityError,
				Reason:   "edge_rule count exceeds per-app cap",
				Field:    "edge_rules",
				Observed: AsAny(wanted),
				Limit:    AsAny(limits.EdgeRulesPerApp),
			})
		}
		// Kind gate: jwt / ip are paid-tier features.
		for _, r := range pending.EdgeRules {
			switch r.Kind {
			case "jwt":
				if !limits.EdgeRulesJWTAllowed {
					out = append(out, Break{
						Code:     api.CodePlanEdgeRuleKindNotAllowed,
						Severity: SeverityError,
						Reason:   "kind=jwt is not enabled on this plan",
						Field:    "edge_rule.kind",
					})
				}
			case "ip":
				if !limits.EdgeRulesIPAllowed {
					out = append(out, Break{
						Code:     api.CodePlanEdgeRuleKindNotAllowed,
						Severity: SeverityError,
						Reason:   "kind=ip is not enabled on this plan",
						Field:    "edge_rule.kind",
					})
				}
			}
		}
		// Per-account edge rule count is not currently capped in
		// pkg/api/limits.go — skip the per-account check.
	}

	// Env var caps (issue #395 / ADR-045).
	if pending.EnvByScope != nil {
		total := 0
		for _, rows := range pending.EnvByScope {
			total += len(rows)
		}
		if limits.EnvVarsMax > 0 && total > limits.EnvVarsMax {
			out = append(out, Break{
				Code:     api.CodePlanLimitEnvVars,
				Severity: SeverityError,
				Reason:   "env var count exceeds per-app cap",
				Field:    "environment",
				Observed: AsAny(total),
				Limit:    AsAny(limits.EnvVarsMax),
			})
		}
		// Per-value byte cap. PendingEnv.Value is the would-write
		// plaintext; the wire's list path never echoes values per
		// ADR-053 §Decision 4.
		for scope, rows := range pending.EnvByScope {
			for _, r := range rows {
				if limits.EnvValueMaxBytes > 0 && len(r.Value) > limits.EnvValueMaxBytes {
					out = append(out, Break{
						Code:     api.CodeEnvVarValueTooLarge,
						Severity: SeverityError,
						Reason:   "env var value exceeds per-value byte cap",
						Field:    "environment." + scope + "." + r.Key,
						Observed: AsAny(len(r.Value)),
						Limit:    AsAny(limits.EnvValueMaxBytes),
					})
				}
			}
		}
	}

	return out
}
