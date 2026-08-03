// Package main — Tier A7 PR-A wire-up keeps.
//
// PR-A of the gatewayd-public / gatewayd-internal split (ADR-070)
// is a pure file move: every handler, consumer, and helper that
// previously lived in cmd/gatewayd/ has been moved verbatim into
// this package, and cmd/gatewayd/ has been deleted. The handler
// graph (PGBackend + Handler + AppLogsHandler + scheddRouter +
// auth_adapters + topN sampler + warm-hint consumer + nodecache +
// egress_grpc) is NOT yet wired into a daemon: that is PR-B's
// job. The cmd/gatewayd-internal/main.go placeholder skeleton
// starts the unix-socket listener + a /readyz probe + a banner
// handler, but it never invokes any of the moved symbols.
//
// The golangci-lint `unused` checker fires on every moved
// symbol that nothing in the package references. We do NOT want
// to either (a) remove the symbols (they're part of the
// file-move surface) or (b) leave CI red (PR-A is supposed to
// land clean). The smallest possible fix is a single file
// listing each moved symbol via `var _ = fn; var _ = T` so the
// linter sees the reference, while the production wire-up
// (cmd/gatewayd-internal/run.go) lands in PR-B and replaces
// these keeps with real consumers.
//
// The keeps are mechanical: every public symbol in the package
// that the linter flags as unused gets one entry. Adding a new
// moved file? Add the keep at the bottom of this file in the
// same PR. Removing a moved file? Drop the keep at the same
// time. The CI gate will catch any drift
// (TestKeepsCoverAllMovedPackageSymbols in wireup_keeps_test.go).
package main

// app_logs.go
var _ = (*AppLogsHandler).ServeHTTP

// applogs_resolver.go
var _ = appLogsScheddResolver{}.ScheddForApp

// audit.go
var _ = (*gatewaydAuditor).Emit

// auth_adapters.go
var _ = storeAsAuthenticator
var _ = authAdapter{}
var _ = authAdapter{}.AuthenticateKey
var _ = authAdapter{}.AccountByID
var _ = authAdapter{}.AppBySlug
var _ = authAdapter{}.TouchKeyLastUsed
var _ = storeAsSessionLookup
var _ = sessionLookupAdapter{}
var _ = sessionLookupAdapter{}.GetSession
var _ = sessionLookupAdapter{}.TouchSessionLastSeen
var _ = auditorAsAuthAuditor
var _ = auditAdapter{}
var _ = auditAdapter{}.Emit

// backend.go
var _ = watchInvalidations
var _ = pgRouter{}.ResolveHost

// config.go
var _ = Config{}
var _ = TOMLTLSConfig{}
var _ = LoadConfig
var _ = (*Config).LoadVMMDPingTLS
var _ = (*Config).LoadEgressTLS

// egress_grpc.go
var _ = defaultEgressGRPCSocketPath
var _ = egressGRPCSocketMode
var _ = egressGRPCSocketPath
var _ = egressGRPCListener{}
var _ = newEgressGRPCListener
var _ = isUnixSocketPath
var _ = (*egressGRPCListener).start
var _ = (*egressGRPCListener).stop

// githubd_proxy.go
var _ = osGetenv
var _ = (*githubdProxy).ServeHTTP

// lastseen.go
var _ = lastSeenFlushInterval
var _ = (*schedFlushSink).Touch
var _ = (*schedFlushSink).Get
var _ = (*schedFlushSink).Forget
var _ = (*schedFlushSink).Flush

// nodecache.go
var _ = newNodeCache
var _ = (*nodeCache).WithEvents
var _ = (*nodeCache).Forwarding
var _ = (*nodeCache).Close
var _ = (*nodeCache).WatchEvictions

// proxy.go
var _ = (*apidProxy).ServeHTTP

// scheddrouter.go
var _ ScheddDialer
var _ = DefaultScheddDialer
var _ ScheddNodeResolver
var _ = newScheddRouter
var _ = (*scheddRouter).ScheddForApp
var _ = (*scheddRouter).ScheddForInstance
var _ = (*scheddRouter).Evict
var _ = (*scheddRouter).Close
var _ = (*scheddRouter).WatchNodeChanges

// secrets.go
var _ = ErrInsecureSecretPerms
var _ = allowedSecretPerm
var _ = loadSecretFile

// session_key.go
var _ = loadSessionManager

// topn.go
var _ = topAccountOtherLabel
var _ = topNSamplerInterval
var _ = topAccountSetCap
var _ = topAccountWindow
var _ = newTopAccountSet
var _ = (*topAccountSet).sample
var _ = (*topAccountSet).snapshotCounts
var _ = (*topAccountSet).topNSnapshot
var _ = (*topAccountSet).shouldReset
var _ = (*topAccountSet).resetWindow
var _ = sortEntries
var _ topNEntry
var _ = topNSampler{}
var _ = newTopNSampler
var _ = (*topNSampler).Sample
var _ = (*topNSampler).run
var _ = (*topNSampler).tick

// warmhints.go
var _ = newWarmHintConsumer
var _ = warmHintConsumer{}
var _ = (*warmHintConsumer).Run
var _ = (*warmHintConsumer).drain
var _ = sleepCtx
var _ = nextBackoff
