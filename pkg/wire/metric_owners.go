package wire

import "github.com/prometheus/client_golang/prometheus"

// daemonForPrefix maps each NewOpsMetrics prefix a production daemon passes
// to the daemon label that binary reports in its identity and readiness
// gauges. Single-owner collectors and identity rows are filtered only for
// these prefixes; any other prefix (tests, tools) keeps every collector and
// every daemon row.
var daemonForPrefix = map[string]string{
	"apid": "apid", "builderd": "builderd", "gatewayd": "gatewayd-internal",
	"gatewayd_public": "gatewayd-public", "githubd": "githubd", "imaged": "imaged",
	"meterd": "meterd", "outboundd": "outboundd", "profiled": "profiled",
	"realtimed": "realtimed", "s3_gateway": "s3-gatewayd", "schedd": "schedd",
	"vmmd": "vmmd",
}

// daemonLabelNames is the closed daemon label set of the identity and
// readiness gauges; any other name reports as "other".
var daemonLabelNames = []string{
	"apid", "gatewayd-public", "gatewayd-internal", "schedd", "vmmd", "imaged",
	"meterd", "builderd", "githubd", "outboundd", "profiled", "realtimed",
	"s3-gatewayd", "gregale",
}

// daemonLabel bounds a daemon name to daemonLabelNames.
func daemonLabel(name string) string {
	if containsString(daemonLabelNames, name) {
		return name
	}
	return "other"
}

// identityDaemons is the daemon label set whose build-info, uptime and
// readiness rows NewOpsMetrics pre-creates. A daemon pre-creates only its
// own: rows for the other daemons read as a second binary that is never
// ready, has zero uptime and runs this binary's version.
func identityDaemons(prefix string) []string {
	if own, ok := daemonForPrefix[prefix]; ok {
		return []string{own}
	}
	return append(append([]string(nil), daemonLabelNames...), "other")
}

// withoutForeignCollectors drops the collectors in owners whose owner list
// does not include prefix. Registering a collector on a daemon that never
// writes it still exports every pre-instantiated label set as zero-valued
// series, so a single-owner histogram costs as much on the ten daemons that
// ignore it as on the one that writes it.
func withoutForeignCollectors(prefix string, collectors []prometheus.Collector, owners map[prometheus.Collector][]string) []prometheus.Collector {
	if _, ok := daemonForPrefix[prefix]; !ok {
		return collectors
	}
	kept := collectors[:0:0]
	for _, c := range collectors {
		if daemons, owned := owners[c]; owned && !containsString(daemons, prefix) {
			continue
		}
		kept = append(kept, c)
	}
	return kept
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
