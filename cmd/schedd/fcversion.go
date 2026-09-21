package main

import (
	"context"
	"log/slog"
)

// fcVersionPinEnv pins the Firecracker version schedd compares snapshots
// against, instead of detecting it from the running binary.
//
// It exists for KVM-free acceptance. `firecracker --version` cannot run on a
// CI runner, so detection fails, the version stays "", and then every snapshot
// row fails Engine.snapshotCompatible's `snap.FCVersion != e.fcVer` check.
// That silently forces EVERY wake down the cold-boot edge — which is why the
// restore path and the ADR-005 staleness contract were unreachable from any
// test without /dev/kvm, and why the e2e suite has only ever exercised cold
// boot. Pinning the version makes both observable without weakening either:
// the comparison itself is untouched, and a mismatched pin still cold-boots.
//
// Dev-only on purpose (see pkg/daemonunitspec/envcontract.go). On a production
// host the running binary is the only truthful source. An operator who pinned
// a wrong value here would make schedd restore snapshots into an incompatible
// Firecracker, which is exactly the corruption ADR-005 exists to prevent.
const fcVersionPinEnv = "FAAS_SCHEDD_FC_VERSION"

// resolveFCVersion returns the Firecracker version the engine pins snapshots
// to (ADR-005). A non-empty pin wins and skips detection; otherwise the
// detector runs and a failure degrades to "", which treats every snapshot as
// incompatible and cold-boots.
func resolveFCVersion(
	ctx context.Context,
	pinned string,
	detect func(context.Context) (string, error),
	log *slog.Logger,
) string {
	if pinned != "" {
		log.Warn("firecracker version pinned by "+fcVersionPinEnv+"; detection skipped. This must never be set on a production host (ADR-005)",
			"fc_version", pinned)
		return pinned
	}
	version, err := detect(ctx)
	if err != nil {
		log.Warn("could not detect firecracker version; treating all snapshots as stale", "err", err)
		return ""
	}
	return version
}
