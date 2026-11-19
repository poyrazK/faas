// Package origdst recovers the pre-DNAT destination of an accepted TCP
// connection from conntrack (SO_ORIGINAL_DST). It is its own package so a
// small daemon, such as the agent egress proxy, can use it without
// pkg/gateway.
package origdst
