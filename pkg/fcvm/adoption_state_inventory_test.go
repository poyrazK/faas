package fcvm

// adr: 20261017 vmmd restart adoption

import (
	"reflect"
	"sort"
	"testing"
)

// vmmd restart adoption ADR §2 "Maps": every map field of Manager and
// JailerVMM is in-memory state a restart loses. This test fails when a new
// one is added without saying what adoption does with it, so new state
// cannot silently escape adoption and misbehave only after a restart.
//
//	rebuilt   adoption restores it for an adopted guest
//	reset     starting empty after a restart is correct (caches, in-flight
//	          operations, per-process replay windows, timers)
//	repushed  the scheduler pushes it again after adoption (slice 3)
//	refused   adoption refuses guests that depend on it (slice 2 scope)
//	bootonly  daemon wiring, or only for kinds that are never adopted
var adoptionStateInventory = map[string]map[string]string{
	"Manager": {
		"appCPUPolicies":                    "repushed", // per-app CPU policy revisions; the lease keeps the applied quota
		"appEgressPolicies":                 "repushed", // egress policy revisions; per-netns rules survive in the netns
		"appResolved":                       "reset",    // DNS-gated egress resolution cache
		"backingDigests":                    "reset",    // file digest cache
		"baseGenerations":                   "reset",    // base image generation cache
		"cidToID":                           "rebuilt",
		"cooldownByDeployment":              "reset",    // crash-loop cooldown timestamps
		"exportDirs":                        "bootonly", // builders are killed at adoption, never adopted
		"frameworkReadyRuns":                "rebuilt",  // loop restarted when readiness was not yet stamped
		"instanceBacking":                   "rebuilt",
		"instanceFlights":                   "reset", // no operation is in flight at startup; intents are slice 3
		"instanceStops":                     "reset",
		"live":                              "rebuilt",
		"nativeRecovered":                   "bootonly", // native process recovery only
		"pendingCleanup":                    "reset",    // interrupted teardown resumes from its stop intent (slice 3)
		"pendingProcessExits":               "reset",
		"perAppAllowlist":                   "rebuilt",
		"perAppStaticIP":                    "refused",
		"perVMHostIP":                       "refused",
		"privateNetworkFabric":              "reset", // idempotency cache; schedd's reconcile probes the kernel
		"privateNetworkTransport":           "reset",
		"privateNetworkTransportPeers":      "reset", // forces one peer sync after a restart, by design
		"processGenerations":                "rebuilt",
		"processStartedAt":                  "rebuilt",
		"qualificationConfigReceiptWaiters": "bootonly", // qualification guests are never adopted
		"qualificationInstances":            "bootonly",
		"quietStates":                       "reset", // quiet pause waits one idle period after adoption
		"readinessLoopCancels":              "rebuilt",
		"resourceLinks":                     "rebuilt",
		"resourceNetworks":                  "rebuilt",
		"restartQuarantine":                 "rebuilt", // adoption removes the adopted guest
		"runtimeAdmissionFlights":           "reset",
		"runtimeAdmissionInstances":         "reset", // per-incarnation replay window; receipts are re-bound (slice 3)
		"runtimeAdmissionTokens":            "reset",
		"staticEgressAliases":               "refused",
		"waking":                            "reset",
		"computeLeases":                     "bootonly", // financial compute authority: wired only with native process recovery
		"functionMigrationDestinations":     "bootonly", // function VM resources: native qualification builds only; adoption refuses function guests there
		"functionMigrationSources":          "bootonly",
		"functionStarting":                  "bootonly",
		"provenWorkers":                     "reset",   // costs one full characterization window per deployment
		"rehearsalRecorders":                "refused", // rehearsal guests are refused (block network.rehearsal_run_id)
	},
	"JailerVMM": {
		"bindMounts":               "rebuilt",
		"bindSourceModes":          "rebuilt",
		"characterizationReceipts": "bootonly", // awaited only during a boot
		"clients":                  "rebuilt",  // created on first use
		"cpuBoostTails":            "reset",    // adoption writes the steady fence instead
		"fences":                   "rebuilt",
		"guestVsockListeners":      "rebuilt",
		"jobExits":                 "rebuilt",  // a job's exit receiver; a recorded exit is in the block
		"storedJobExits":           "rebuilt",  // taken back from systemd's fd store
		"guestVsockStreamHandlers": "bootonly", // registered by daemon wiring
		"materialisedIdentity":     "rebuilt",  // from checkpointed materialised and clone assets
		"materialisedTmp":          "rebuilt",
		"nativeQualificationRestoreStreamHandlers": "bootonly",
		"nativeRestoreChannels":                    "bootonly",
		"ownedJails":                               "rebuilt",
		"proc":                                     "rebuilt",
		"recs":                                     "rebuilt",
		"rings":                                    "rebuilt",
		"runtimeDriveHandoffs":                     "reset",    // the handoff descriptors closed with the predecessor
		"consoleOpens":                             "reset",    // console files being pre-opened for a boot in flight
		"guestVsockLeaseHandlers":                  "bootonly", // registered by daemon wiring; adoption prepares the guest's listeners
		"instanceLogStops":                         "rebuilt",  // adoption registers the app ring, which starts the observer
	},
}

// adoptionInstanceInventory classifies every Instance field the same way.
// adoptedInstance rebuilds an Instance from the adoption block, so a field
// missing from the block is lost for every adopted guest; two such losses
// (the ADR-740 live-patch mark and the migration pause) were bugs.
var adoptionInstanceInventory = map[string]string{
	"nativeGeneration":                "bootonly", // native process recovery only
	"operatorRestartGeneration":       "reset",    // issued on demand; a stale one is refused and the operator flow re-reads it
	"Lease":                           "rebuilt",
	"Net":                             "rebuilt",
	"Method":                          "rebuilt",
	"runtimeAdmissionReceipt":         "rebuilt", // the block's admission (ADR-595 extension)
	"runtimeAdmissionEgress":          "rebuilt",
	"admissionHeldFrom":               "rebuilt",
	"ExecutionOnly":                   "refused", // execution kind
	"ExecutionID":                     "refused",
	"ExecutionLeaseToken":             "refused",
	"ExecutionOutboundIntegrationIDs": "refused",
	"AppTaskOnly":                     "refused", // app-task kind
	"QualificationOnly":               "refused", // qualification kind
	"Paused":                          "rebuilt", // warm-pool restore, claimed or not
	"QuietPauseAfter":                 "rebuilt",
	"Frozen":                          "rebuilt",
	"FrozenSince":                     "rebuilt",
	"freezing":                        "reset",   // no freeze is in flight at startup
	"IsJob":                           "rebuilt", // job kind
	"JobStartGateEnabled":             "rebuilt",
	"JobInteractive":                  "rebuilt",
	"jobExit":                         "rebuilt",
	"RestoreMs":                       "reset", // wake telemetry
	"NetnsTapMs":                      "reset",
	"GuestReadyMs":                    "reset",
	"RestoreError":                    "reset",
	"RestoreFallbackReason":           "reset",
	"AppID":                           "rebuilt",
	"AccountID":                       "rebuilt",
	"DeploymentID":                    "rebuilt",
	"LivenessProbe":                   "rebuilt",
	"ReadinessProbe":                  "rebuilt",
	"AllowlistHandleV4":               "rebuilt",
	"AllowlistHandleV6":               "rebuilt",
	"PrivateNetworkHandleV4":          "rebuilt",
	"PrivateNetworkHandleV6":          "rebuilt",
	"Plan":                            "rebuilt",
	"Port":                            "rebuilt",
	"HealthcheckPath":                 "rebuilt",
	"ImageHealthcheckRequired":        "rebuilt",
	"StartupDeadlineS":                "rebuilt",
	"ExecutionMode":                   "rebuilt",
	"WorkloadNames":                   "rebuilt",
	"Characterization":                "rebuilt", // ObservedClass only: the liveness contract of an undeclared worker
	"Runtime":                         "rebuilt",
	"FrameworkReadyAt":                "rebuilt",
	"DiskUsedBytes":                   "reset", // sampled again
	"DiskCapacityBytes":               "reset",
	"DiskPressure":                    "reset",
	"DiskSampledAt":                   "reset",
	"EgressNewDstPerMin":              "reset",
	"EgressFanoutValid":               "reset",
	"EgressFloodDropsPerMin":          "reset",
	"resolvedEgress":                  "reset", // cache; the nft entries keep their own timeouts
	"TailCount":                       "reset", // the host only ever sees decrements
	"tailSecondsAccum":                "reset", // informational, not billed
	"adoptionVCPUs":                   "rebuilt",
	"adoptionMemSizeMiB":              "rebuilt",
	"adoptionOp":                      "reset", // resolved before adoption (slice 3)
	"devPatched":                      "rebuilt",
	"migrationPaused":                 "rebuilt",
	"warmSpare":                       "rebuilt",
	"DataRepairOnly":                  "refused", // a data repair runs as an execution, which adoption kills
	"HealthcheckGRPC":                 "rebuilt",
	"functionDestination":             "bootonly", // function VM resources: native qualification builds only
	"functionExecutions":              "bootonly",
	"functionMigration":               "bootonly",
	"publicForwardTarget":             "bootonly", // captured only under native process recovery
	"wakeID":                          "bootonly", // function VM migration authority
}

func TestAdoptionStateInventoryClassifiesEveryInstanceField(t *testing.T) {
	valid := map[string]bool{"rebuilt": true, "reset": true, "repushed": true, "refused": true, "bootonly": true}
	typ := reflect.TypeOf(Instance{})
	seen := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		seen[name] = true
		class, ok := adoptionInstanceInventory[name]
		switch {
		case !ok:
			t.Errorf("Instance.%s is new per-guest state: classify it in adoptionInstanceInventory and record it in the adoption block if adopted guests need it", name)
		case !valid[class]:
			t.Errorf("Instance.%s: unknown class %q", name, class)
		}
	}
	for name := range adoptionInstanceInventory {
		if !seen[name] {
			t.Errorf("Instance.%s is classified but no longer a field; remove it", name)
		}
	}
}

func TestAdoptionStateInventoryClassifiesEveryMap(t *testing.T) {
	valid := map[string]bool{"rebuilt": true, "reset": true, "repushed": true, "refused": true, "bootonly": true}
	for _, typ := range []reflect.Type{reflect.TypeOf(Manager{}), reflect.TypeOf(JailerVMM{})} {
		classes := adoptionStateInventory[typ.Name()]
		seen := map[string]bool{}
		var missing []string
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if f.Type.Kind() != reflect.Map {
				continue
			}
			seen[f.Name] = true
			class, ok := classes[f.Name]
			if !ok {
				missing = append(missing, f.Name)
				continue
			}
			if !valid[class] {
				t.Errorf("%s.%s: unknown class %q", typ.Name(), f.Name, class)
			}
		}
		sort.Strings(missing)
		for _, name := range missing {
			t.Errorf("%s.%s is new in-memory state: classify it in adoptionStateInventory and handle it in adoption (manager_adopt.go)", typ.Name(), name)
		}
		for name := range classes {
			if !seen[name] {
				t.Errorf("%s.%s is classified but no longer a map field; remove it", typ.Name(), name)
			}
		}
	}
}
