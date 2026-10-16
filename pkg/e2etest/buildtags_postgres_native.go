//go:build metal && managed_postgres_native

package e2etest

const daemonBuildTags = "metal,managed_postgres_native" + functionLifetimeDaemonTags
