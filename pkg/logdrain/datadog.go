package logdrain

import (
	"strings"
	"time"
)

// datadogLog is one entry for Datadog's HTTP logs intake (ADR-742). The
// reserved attributes (message, ddsource, service, hostname, ddtags, status)
// are what Datadog indexes and correlates on; the rest are custom attributes.
type datadogLog struct {
	Message      string `json:"message"`
	DDSource     string `json:"ddsource"`
	Service      string `json:"service,omitempty"`
	Hostname     string `json:"hostname,omitempty"`
	DDTags       string `json:"ddtags,omitempty"`
	Status       string `json:"status"`
	Timestamp    string `json:"timestamp"`
	AppID        string `json:"app_id"`
	DeploymentID string `json:"deployment_id,omitempty"`
	RequestID    string `json:"request_id,omitempty"`
	TraceID      string `json:"trace_id,omitempty"`
	Stream       string `json:"stream"`
	Sequence     uint64 `json:"sequence"`
}

const datadogSource = "gregale"

func makeDatadogLog(service string, r Record) datadogLog {
	status := "info"
	if r.Stream == "stderr" {
		status = "error"
	}
	var tags []string
	if env := datadogTagValue(r.Environment); env != "" {
		tags = append(tags, "env:"+env)
	}
	if version := datadogTagValue(datadogVersion(r)); version != "" {
		tags = append(tags, "version:"+version)
	}
	if dep := datadogTagValue(r.DeploymentID); dep != "" {
		tags = append(tags, "deployment_id:"+dep)
	}
	if region := datadogTagValue(r.Region); region != "" {
		tags = append(tags, "region:"+region)
	}
	return datadogLog{
		Message: r.Line, DDSource: datadogSource, Service: datadogTagValue(service),
		Hostname: r.InstanceID, DDTags: strings.Join(tags, ","), Status: status,
		Timestamp: r.WrittenAt.UTC().Format(time.RFC3339Nano),
		AppID:     r.AppID, DeploymentID: r.DeploymentID, RequestID: r.RequestID, TraceID: r.TraceID,
		Stream: r.Stream, Sequence: r.Sequence,
	}
}

// datadogVersion prefers the human deployment tag, then a short commit.
func datadogVersion(r Record) string {
	if r.DeploymentTag != "" {
		return r.DeploymentTag
	}
	if len(r.CommitSHA) > 12 {
		return r.CommitSHA[:12]
	}
	return r.CommitSHA
}

// datadogTagValue normalises a value to Datadog's tag rules: lowercase,
// alphanumerics plus _-./ only, at most 200 characters. Anything else becomes
// an underscore so customer-controlled values cannot inject extra tags.
func datadogTagValue(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	var b strings.Builder
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-', r == '.', r == '/':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
		if b.Len() >= 200 {
			break
		}
	}
	return b.String()
}
