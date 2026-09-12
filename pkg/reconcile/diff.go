// Diff between a fresh reposcan.Result and the project's existing
// membership. The merge key is reposcan.Workload.Key() =
// (RootDir, Name). Two members with the same key collapse into
// one Action — there is no per-source fan-out.
//
// Copying the priority list from cmd/apid/scan_service.go:474-491
// was the only way to produce a state.ProjectScanSource from a
// reposcan.Result without importing cmd/apid (a cyclic dep —
// pkg/reconcile must stay vendorable by cmd/githubd). The two
// copies diverge only if the detector fan-out in pkg/reposcan
// changes; the unit test TestReconcile_DeriveScanSource_MirrorsApid
// pins the priority list verbatim so the divergence shows up at
// PR-review time.

package reconcile

import (
	"strings"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// Action is the canonical reconcile primitive. Created by
// workloadDiff, consumed by applyActions. Fields:
//
//   - Op: "create" | "update" | "remove"
//   - Workload: the reposcan-side input (zero on remove)
//   - App: the state-side row (zero on create)
//   - StartCommand: the resolved start_command override (empty on
//     remove). Pre-resolved here so apply.go never has to inspect
//     Workload.Command.
//
// fieldsChanged is populated by applyActions on a successful
// update so the audit row can carry the structured diff.
type Action struct {
	Op            string
	Workload      reposcan.Workload
	App           state.App
	StartCommand  string
	fieldsChanged []string
}

// workloadDiff produces the create/update/remove set for a
// reconcile. existing is the result of store.AppsForProject —
// already filtered to status <> 'deleted' by the store layer
// (store.go:608-612). exclude is the operator --exclude set,
// keyed by lowercased WorkloadName; any existing app whose
// lowercased WorkloadName is in exclude is DROPPED before the
// removes loop, so the diff never emits a remove Action for an
// operator-excluded app. Code-review finding #1 (PR #1065): the
// earlier version didn't honor exclude, so `--exclude=foo` for an
// existing app silently soft-deleted the app via workloadDiff's
// removes loop + applyActions.applyRemove → SoftDeleteAppCascade.
// The order of the returned slice is stable: creates first
// (sorted by workload.Name), then updates (sorted by
// app.WorkloadName), then removes (sorted by app.WorkloadName).
// Stable ordering is testable and, more importantly, makes the
// audit log deterministic across retries.
//
// The diff is INTENTIONALLY conservative: a (RootDir, WorkloadName)
// present in both scan and existing but with identical columns is
// NOT emitted as an update. The update path only fires when at
// least one of RootDir / WorkloadName / StartCommand actually
// changed. This keeps the audit log clean and avoids spurious
// project.workload.changed rows on every scan.
func workloadDiff(
	scan reposcan.Result,
	_ state.Project,
	existing []state.App,
	exclude map[string]bool,
) []Action {
	// Filter existing apps whose WorkloadName is in exclude.
	// Without this filter, the removes loop below would emit a
	// remove Action for every existing app that the scan
	// (already --exclude-filtered upstream) doesn't carry, and
	// applyActions.applyRemove would SoftDeleteAppCascade the
	// app the operator explicitly asked to leave alone.
	if len(exclude) > 0 {
		filtered := make([]state.App, 0, len(existing))
		for _, a := range existing {
			if exclude[strings.ToLower(a.WorkloadName)] {
				continue
			}
			filtered = append(filtered, a)
		}
		existing = filtered
	}
	// Build the (RootDir, Name) index of existing apps.
	existingByKey := make(map[workloadKey]state.App, len(existing))
	for _, a := range existing {
		existingByKey[workloadKey{RootDir: a.RootDir, Name: a.WorkloadName}] = a
	}

	// Build the (RootDir, Name) index of scan workloads.
	scanByKey := make(map[workloadKey]reposcan.Workload, len(scan.Workloads))
	for _, w := range scan.Workloads {
		scanByKey[workloadKey{RootDir: w.RootDir, Name: w.Name}] = w
	}

	var creates []Action
	var updates []Action
	var removes []Action

	for _, w := range scan.Workloads {
		key := workloadKey{RootDir: w.RootDir, Name: w.Name}
		a, ok := existingByKey[key]
		if !ok {
			creates = append(creates, Action{
				Op:           "create",
				Workload:     w,
				StartCommand: resolveStartCommand(w),
			})
			continue
		}
		desired := resolveStartCommand(w)
		changed := diffFieldsChanged(a, w, desired, workloadNameSet(scan.Workloads))
		if len(changed) == 0 {
			// No-op: same key, same columns. Skip.
			continue
		}
		updates = append(updates, Action{
			Op:            "update",
			Workload:      w,
			App:           a,
			StartCommand:  desired,
			fieldsChanged: changed,
		})
	}

	for _, a := range existing {
		key := workloadKey{RootDir: a.RootDir, Name: a.WorkloadName}
		if _, ok := scanByKey[key]; ok {
			continue
		}
		removes = append(removes, Action{
			Op:  "remove",
			App: a,
		})
	}

	// Stable ordering. The diff's order is observable in the audit log
	// and drives the sequential project build enqueue path. Prefer a
	// dependency order for creates; fall back to lexical order for a
	// direct caller that bypassed admission validation.
	if order, err := reposcan.DependencyOrder(scan.Workloads, scan.Managed); err == nil {
		sortActionsByDependencyOrder(creates, order)
	} else {
		sortActionsByName(creates, true)
	}
	sortActionsByName(updates, false)
	sortActionsByName(removes, false)

	out := make([]Action, 0, len(creates)+len(updates)+len(removes))
	out = append(out, creates...)
	out = append(out, updates...)
	out = append(out, removes...)
	return out
}

func sortActionsByDependencyOrder(as []Action, order []string) {
	position := make(map[string]int, len(order))
	for i, name := range order {
		position[strings.ToLower(name)] = i
	}
	for i := 1; i < len(as); i++ {
		for j := i; j > 0; j-- {
			left, okLeft := position[strings.ToLower(as[j-1].Workload.Name)]
			right, okRight := position[strings.ToLower(as[j].Workload.Name)]
			if !okLeft {
				left = len(order)
			}
			if !okRight {
				right = len(order)
			}
			if left > right {
				as[j-1], as[j] = as[j], as[j-1]
				continue
			}
			break
		}
	}
}

func sortActionsByName(as []Action, byWorkload bool) {
	// Insertion sort: small slices, stable, no extra deps. The
	// worst case is ~100 members (Scale plan cap) so O(n²) is
	// fine.
	for i := 1; i < len(as); i++ {
		for j := i; j > 0; j-- {
			var lhs, rhs string
			if byWorkload {
				lhs = as[j-1].Workload.Name
				rhs = as[j].Workload.Name
			} else {
				lhs = as[j-1].App.WorkloadName
				rhs = as[j].App.WorkloadName
			}
			if strings.ToLower(lhs) > strings.ToLower(rhs) {
				as[j-1], as[j] = as[j], as[j-1]
				continue
			}
			break
		}
	}
}

// resolveStartCommand flattens the workload's Command[] into the
// single string the apps.start_command column stores. Empty
// command → empty string. PR-E's UpdateAppParams treats empty
// string as NULL for the nullable column, so callers don't have
// to think about the NULL vs "" distinction.
func resolveStartCommand(w reposcan.Workload) string {
	if len(w.Command) == 0 {
		return ""
	}
	// Spec §11: a command that contains a secret value (in
	// practice: never; start_command is a process-spec, not a
	// value) must NEVER be logged. We log the length, not the
	// content, in the audit row.
	return strings.Join(w.Command, " ")
}

// diffFieldsChanged returns the subset of {"root_dir", "workload_name",
// "start_command", "service_env"} that actually changed between the existing
// state.App and the new scan-derived workload. The columns
// RootDir and WorkloadName are NOT NULL DEFAULT ” in the schema
// so equality is on the empty-string vs populated distinction —
// no NULL handling needed.
func diffFieldsChanged(a state.App, w reposcan.Workload, startCmd string, available ...map[string]struct{}) []string {
	var changed []string
	if a.RootDir != w.RootDir {
		changed = append(changed, "root_dir")
	}
	if a.WorkloadName != w.Name {
		changed = append(changed, "workload_name")
	}
	// StartCommand is NULL-able; the existing App has "" for the
	// unset case (memstore mirrors pgstore via the NULL → ""
	// mapping in the store's read path). Compare on the string
	// value.
	if a.StartCommand != startCmd {
		changed = append(changed, "start_command")
	}
	var serviceNames map[string]struct{}
	if len(available) > 0 {
		serviceNames = available[0]
	}
	if !serviceEnvEqual(a.Manifest.Env, serviceEnvForWorkloadWithAvailable(nil, w, serviceNames)) {
		changed = append(changed, "service_env")
	}
	return changed
}

func workloadNameSet(workloads []reposcan.Workload) map[string]struct{} {
	set := make(map[string]struct{}, len(workloads))
	for _, workload := range workloads {
		set[strings.ToLower(strings.TrimSpace(workload.Name))] = struct{}{}
	}
	return set
}

// DeriveScanSource picks the project scan_source from the
// workloads that survived any earlier filter. The priority list
// here is the canonical one — apid's scan_service previously
// duplicated it; PR-GH.2 retires the apid copy and routes through
// this exported function instead.
//
// Marker returns ProjectScanSourceSingle when exactly one workload
// survives and that workload is the root-floor (RootDir == "" +
// Name == "app"). detector tag is "root-floor" in that case, not
// in the priority list, so it falls through to the len(workloads)==1
// branch.
//
// Exported so apid (PR-GH.2) and any future caller can derive the
// project scan_source without importing cmd/apid (which would
// create a cyclic dep through pkg/reconcile).
// composeSourceFilenames is the set of filenames the compose
// detector emits as the source prefix. The detector writes
// `src + ": " + name` (pkg/reposcan/compose.go:148) where src is
// the actual manifest filename, so the priority list (which
// probes the detector-class name "compose") must additionally
// probe each filename. Without this, a repo with one
// docker-compose.yml service falls through to len==1 → "single"
// instead of "compose", and any later re-apply that grows to N
// workloads trips the monotonic-upgrade guard with a downgrade
// to "unknown".
var composeSourceFilenames = []string{
	"docker-compose.yml",
	"docker-compose.yaml",
	"compose.yml",
	"compose.yaml",
}

func DeriveScanSource(workloads []reposcan.Workload) state.ProjectScanSource {
	// Priority order matches the detector fan-out in
	// pkg/reposcan/scan.go:145-156. The first match wins.
	//
	// The compose detector emits source strings starting with the
	// actual manifest filename (e.g. "docker-compose.yml: api"),
	// not a literal "compose:" prefix. We probe both the
	// detector-class name and the filename set per entry so the
	// priority list reads as detector classes, not filenames.
	priority := []string{
		"compose", "procfile", "k8s", "render", "fly",
		"serverless", "app.yaml", "workspaces", "convention",
	}
	for _, want := range priority {
		for _, w := range workloads {
			if matchDetectorSource(want, w.Source) {
				return state.ProjectScanSource(want)
			}
		}
	}
	if len(workloads) == 1 {
		return state.ProjectScanSourceSingle
	}
	return state.ProjectScanSourceUnknown
}

// matchDetectorSource returns true if a workload source string
// was emitted by the named detector class. Most detectors write
// `<name>:<space>…` (e.g. "fly: web", "k8s/deployment.yaml: web");
// the compose detector writes the actual manifest filename
// (e.g. "docker-compose.yml: api"). The compose class is
// matched specially against composeSourceFilenames.
func matchDetectorSource(detector, source string) bool {
	if detector == "compose" {
		for _, fn := range composeSourceFilenames {
			if strings.HasPrefix(source, fn+":") {
				return true
			}
		}
		// Fallback: also accept the bare "compose:" prefix in
		// case a future detector variant emits it.
		return strings.HasPrefix(source, "compose:") || strings.HasPrefix(source, "compose.")
	}
	return strings.HasPrefix(source, detector+":") ||
		strings.HasPrefix(source, detector+".")
}

// tierRank is the state-side monotonic-upgrade ranking. Higher
// rank wins; a downgrade is rejected. Mirrors pkg/state.tierRank
// (which is unexported). We don't import the unexported helper
// because pkg/reconcile is a different package; the values come
// from the canonical mapping in pkg/state. The enum is duplicated
// here to keep this file self-contained for the test reviewers.
func tierRank(s state.ProjectScanSource) int {
	switch s {
	case state.ProjectScanSourceCompose:
		return 8
	case state.ProjectScanSourceK8s:
		return 8
	case state.ProjectScanSourceRender:
		return 8
	case state.ProjectScanSourceFly:
		return 8
	case state.ProjectScanSourceServerless:
		return 8
	case state.ProjectScanSourceProcfile:
		return 6
	case state.ProjectScanSourceWorkspace:
		return 4
	case state.ProjectScanSourceConvention:
		return 2
	case state.ProjectScanSourceSingle:
		return 1
	case state.ProjectScanSourceUnknown:
		return 0
	default:
		return 0
	}
}
