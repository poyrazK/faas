package api

// LogSource identifies the subsystem that produced a customer-visible log
// query event. The first query surface supports the existing runtime stream
// and the durable HTTP request telemetry store.
type LogSource string

const (
	LogSourceRuntime LogSource = "runtime"
	LogSourceHTTP    LogSource = "http"
)
