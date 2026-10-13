package api

// MCPMethodLabel admits a closed vocabulary; method names are caller-supplied
// hints, not verified JSON-RPC body fields. Names and arguments never become labels.
func MCPMethodLabel(method string) string {
	switch method {
	case "initialize", "notifications/initialized", "server/discover", "ping",
		"tools/list", "tools/call", "resources/list", "resources/templates/list", "resources/read", "resources/subscribe", "resources/unsubscribe",
		"prompts/list", "prompts/get", "completion/complete", "tasks/get", "tasks/result", "tasks/list", "tasks/cancel":
		return method
	default:
		return "other"
	}
}

// MCPHTTPOutcome classifies transport evidence without parsing response bodies.
func MCPHTTPOutcome(status int) string {
	switch status {
	case 401:
		return "unauthorized"
	case 403:
		return "forbidden"
	case 400, 405, 406, 411, 413, 415, 422:
		return "invalid_request"
	case 408, 504:
		return "timeout"
	case 429:
		return "rate_limited"
	case 499:
		return "canceled"
	}
	if status >= 500 {
		return "upstream_error"
	}
	if status >= 400 {
		return "http_error"
	}
	if status >= 200 && status < 400 {
		return "http_success"
	}
	return "unknown"
}
