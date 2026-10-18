package api

// MCPMethodLabel admits a closed vocabulary; method names are caller-supplied
// hints, not verified JSON-RPC body fields. Names and arguments never become labels.
func MCPMethodLabel(method string) string {
	switch method {
	case "initialize":
		return "initialize"
	case "notifications/initialized":
		return "notifications/initialized"
	case "server/discover":
		return "server/discover"
	case "ping":
		return "ping"
	case "tools/list":
		return "tools/list"
	case "tools/call":
		return "tools/call"
	case "resources/list":
		return "resources/list"
	case "resources/templates/list":
		return "resources/templates/list"
	case "resources/read":
		return "resources/read"
	case "resources/subscribe":
		return "resources/subscribe"
	case "resources/unsubscribe":
		return "resources/unsubscribe"
	case "prompts/list":
		return "prompts/list"
	case "prompts/get":
		return "prompts/get"
	case "completion/complete":
		return "completion/complete"
	case "tasks/get":
		return "tasks/get"
	case "tasks/result":
		return "tasks/result"
	case "tasks/list":
		return "tasks/list"
	case "tasks/cancel":
		return "tasks/cancel"
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
