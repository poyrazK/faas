package publicstatus

import "time"

type Alert struct {
	Severity string
	Labels   map[string]string
}

type Overlay struct {
	State      State
	Components []Component
}

var daemonComponents = map[string]Component{
	"apid":              ComponentAPIConsole,
	"builderd":          ComponentDeployments,
	"imaged":            ComponentDeployments,
	"githubd":           ComponentDeployments,
	"schedd":            ComponentAppExecution,
	"vmmd":              ComponentAppExecution,
	"gatewayd":          ComponentNetworking,
	"gatewayd-public":   ComponentNetworking,
	"gatewayd-internal": ComponentNetworking,
	"meterd":            ComponentObservability,
	"prometheus":        ComponentObservability,
	"alertmanager":      ComponentObservability,
	"loki":              ComponentObservability,
	"promtail":          ComponentObservability,
}

func ComponentsForAlert(labels map[string]string) []Component {
	component := labels["component"]
	if publicComponent := Component(component); ValidComponent(publicComponent) {
		return []Component{publicComponent}
	}
	if component == "platform" || component == "controlplane" || component == "faas-control-plane" {
		if mapped, ok := daemonComponents[labels["daemon"]]; ok {
			return []Component{mapped}
		}
		return AllComponents()
	}
	if mapped, ok := daemonComponents[component]; ok {
		return []Component{mapped}
	}
	if mapped, ok := daemonComponents[labels["daemon"]]; ok {
		return []Component{mapped}
	}
	return nil
}

func Evaluate(alerts []Alert, overlays []Overlay) map[Component]State {
	out := make(map[Component]State, len(publicComponents))
	for _, component := range publicComponents {
		out[component] = StateOperational
	}
	for _, alert := range alerts {
		var state State
		switch alert.Severity {
		case "warn":
			state = StateDegraded
		case "page":
			state = StatePartialOutage
		default:
			continue
		}
		for _, component := range ComponentsForAlert(alert.Labels) {
			out[component] = Worse(out[component], state)
		}
	}
	return ApplyOverlays(out, overlays)
}

// ApplyOverlays returns a copy of states with operator-authored incidents and
// maintenance applied. Keeping the input immutable matters because callers may
// be holding a cached telemetry snapshot that must become clean again after an
// event is resolved.
func ApplyOverlays(states map[Component]State, overlays []Overlay) map[Component]State {
	out := make(map[Component]State, len(publicComponents))
	for _, component := range publicComponents {
		state, ok := states[component]
		if !ok {
			state = StateUnknown
		}
		out[component] = state
	}
	for _, overlay := range overlays {
		for _, component := range overlay.Components {
			if ValidComponent(component) {
				out[component] = Worse(out[component], overlay.State)
			}
		}
	}
	return out
}

func Worse(a, b State) State {
	if a == StateUnknown {
		return b
	}
	if b == StateUnknown {
		return a
	}
	if severityRank(b) > severityRank(a) {
		return b
	}
	return a
}

func Overall(states map[Component]State, dataAvailable bool) State {
	overall := StateOperational
	known := dataAvailable
	for _, component := range publicComponents {
		state := states[component]
		if state != StateUnknown {
			overall = Worse(overall, state)
			if state != StateOperational {
				known = true
			}
		}
	}
	if !known {
		return StateUnknown
	}
	return overall
}

func severityRank(v State) int {
	switch v {
	case StateMaintenance:
		return 1
	case StateDegraded:
		return 2
	case StatePartialOutage:
		return 3
	case StateMajorOutage:
		return 4
	default:
		return 0
	}
}

type Bucket struct {
	At           time.Time
	State        State
	HasTelemetry bool
}

type DailyObservation struct {
	Date        time.Time
	State       State
	UptimePct   *float64
	CoveragePct float64
	Expected    int
	Observed    int
	Up          int
}

func SummarizeDay(day time.Time, buckets []Bucket, expectedBuckets int) DailyObservation {
	obs := DailyObservation{Date: day.UTC(), State: StateUnknown, Expected: expectedBuckets}
	if expectedBuckets <= 0 {
		return obs
	}
	available := 0
	up := 0
	worst := StateOperational
	for _, bucket := range buckets {
		if !bucket.HasTelemetry {
			continue
		}
		available++
		if bucket.State == StateOperational || bucket.State == StateMaintenance {
			up++
		}
		worst = Worse(worst, bucket.State)
	}
	obs.CoveragePct = float64(available) / float64(expectedBuckets) * 100
	obs.Observed = available
	obs.Up = up
	if available == 0 || obs.CoveragePct < 80 {
		return obs
	}
	uptime := float64(up) / float64(available) * 100
	obs.State = worst
	obs.UptimePct = &uptime
	return obs
}

func ThirtyDayUptime(days []DailyObservation) (uptimePct, coveragePct float64, available bool) {
	if len(days) == 0 {
		return 0, 0, false
	}
	totalExpected := 0
	totalObserved := 0
	totalUp := 0
	for _, day := range days {
		totalExpected += day.Expected
		totalObserved += day.Observed
		totalUp += day.Up
	}
	if totalExpected == 0 {
		return 0, 0, false
	}
	coveragePct = float64(totalObserved) / float64(totalExpected) * 100
	if coveragePct < 80 || totalObserved == 0 {
		return 0, coveragePct, false
	}
	return float64(totalUp) / float64(totalObserved) * 100, coveragePct, true
}
