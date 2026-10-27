package main

// rehearsalContainmentEnabled reports whether this node serves contained
// rehearsals (ADR-20261019120000). Disabled, vmmd wires no durable rehearsal
// policy store: ordinary wake, resume, relay, identity and event-publish paths
// make no rehearsal database reads, and contained wake requests are refused
// because no policy can be confirmed.
func rehearsalContainmentEnabled(getenv func(string) string) bool {
	return getenv("FAAS_REHEARSAL_NETWORK_CONTAINMENT") == "1"
}
