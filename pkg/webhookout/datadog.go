package webhookout

import (
	"encoding/json"
	"strings"
)

// datadogAPIKeyHeader mirrors api.DatadogAPIKeyHeader; pkg/api imports this
// package, so the constant cannot be shared without a cycle.
const datadogAPIKeyHeader = "DD-API-KEY"

// datadogEvent is a Datadog Events API v1 body (ADR-742).
type datadogEvent struct {
	Title          string   `json:"title"`
	Text           string   `json:"text"`
	AlertType      string   `json:"alert_type"`
	Tags           []string `json:"tags,omitempty"`
	AggregationKey string   `json:"aggregation_key,omitempty"`
	SourceTypeName string   `json:"source_type_name"`
	DateHappened   int64    `json:"date_happened"`
}

// datadogEventPayload is the subset of the deployment and rollout webhook
// payloads the Datadog event uses. Unknown events still map generically.
type datadogEventPayload struct {
	DeploymentID   string `json:"deployment_id"`
	Status         string `json:"status"`
	ErrorCode      string `json:"error_code"`
	RolloutState   string `json:"rollout_state"`
	TrafficPercent int    `json:"traffic_percent"`
	Reason         string `json:"reason"`
}

func marshalDatadogEvent(evt Event) ([]byte, error) {
	var p datadogEventPayload
	if len(evt.Data) > 0 {
		_ = json.Unmarshal(evt.Data, &p) // best effort: the title still names the event
	}
	service := evt.Service
	if service == "" {
		service = evt.AppID
	}
	alert, text := "info", ""
	switch evt.Type {
	case "deployment.live":
		alert, text = "success", "Deployment "+shortID(p.DeploymentID)+" is live."
	case "deployment.failed":
		alert, text = "error", "Deployment "+shortID(p.DeploymentID)+" failed"
		if p.ErrorCode != "" {
			text += " (" + p.ErrorCode + ")"
		}
		text += "."
	case "rollout.completed":
		alert, text = "success", "Rollout of deployment "+shortID(p.DeploymentID)+" completed."
	case "rollout.aborted":
		alert, text = "warning", "Rollout of deployment "+shortID(p.DeploymentID)+" aborted"
		if p.Reason != "" {
			text += ": " + p.Reason
		}
		text += "."
	default:
		text = "Gregale event " + evt.Type + "."
	}
	tags := []string{"source:gregale", "event:" + tagValue(evt.Type)}
	if s := tagValue(service); s != "" {
		tags = append(tags, "service:"+s)
	}
	if d := tagValue(p.DeploymentID); d != "" {
		tags = append(tags, "deployment_id:"+d)
	}
	return json.Marshal(datadogEvent{
		Title:          "[" + service + "] " + evt.Type,
		Text:           text,
		AlertType:      alert,
		Tags:           tags,
		AggregationKey: p.DeploymentID,
		SourceTypeName: "gregale",
		DateHappened:   evt.OccurredAt.Unix(),
	})
}

func shortID(id string) string {
	id = strings.ReplaceAll(id, "-", "")
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// tagValue normalises a value to Datadog's tag character set so payload
// values cannot add tags of their own.
func tagValue(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	var b strings.Builder
	for _, r := range v {
		if b.Len() >= 200 {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-', r == '.', r == '/':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}
