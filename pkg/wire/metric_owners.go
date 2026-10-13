package wire

import "github.com/prometheus/client_golang/prometheus"

// daemonMetricPrefixes are the NewOpsMetrics prefixes production daemons
// pass. Single-owner collectors are filtered only for these; any other
// prefix (tests, tools) keeps registering every collector.
var daemonMetricPrefixes = map[string]bool{
	"apid": true, "builderd": true, "gatewayd": true, "gatewayd_public": true,
	"githubd": true, "imaged": true, "meterd": true, "outboundd": true,
	"profiled": true, "realtimed": true, "s3_gateway": true, "schedd": true,
	"vmmd": true,
}

// withoutForeignCollectors drops the collectors in owners whose owner list
// does not include prefix. Registering a collector on a daemon that never
// writes it still exports every pre-instantiated label set as zero-valued
// series, so a single-owner histogram costs as much on the ten daemons that
// ignore it as on the one that writes it.
func withoutForeignCollectors(prefix string, collectors []prometheus.Collector, owners map[prometheus.Collector][]string) []prometheus.Collector {
	if !daemonMetricPrefixes[prefix] {
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
