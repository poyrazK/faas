package sharedruntime

// Env controls building source container apps on the shared runner base
// (docs/adr/1500-shared-runtime-source-apps.md). On by default since the metal
// acceptance run (ADR-20261029103512); "0" turns it off. builderd decides with
// it, and imaged reads it to explain an app layer that outgrew its plan.
const Env = "FAAS_SOURCE_APP_SHARED_RUNTIME"

// Enabled reports whether a node builds source container apps on the shared
// runner base, given that node's value of Env.
func Enabled(value string) bool { return value != "0" }
