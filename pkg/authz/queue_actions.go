package authz

import "strings"

// QueuePattern also closes unreviewed trigger and queue configuration paths.
func QueuePattern(pattern string) bool {
	return strings.Contains(pattern, "/queues") || strings.Contains(pattern, "/queue-bindings") || strings.Contains(pattern, "/queue-workload") || strings.Contains(pattern, "/triggers")
}

func QueueAction(pattern string) (OrgAction, bool) {
	pattern = strings.Replace(pattern, "/v1/orgs/{organization}/apps/", "/v1/apps/", 1)
	action, ok := queueActions[pattern]
	return action, ok
}

var queueActions = map[string]OrgAction{
	"GET /v1/apps/{slug}/queue-bindings":                  OrgActionReadQueues,
	"GET /v1/apps/{slug}/queue-bindings/{id}":             OrgActionReadQueues,
	"GET /v1/apps/{slug}/queue-bindings/{id}/status":      OrgActionReadQueues,
	"POST /v1/apps/{slug}/queue-bindings":                 OrgActionConfigureQueues,
	"PATCH /v1/apps/{slug}/queue-bindings/{id}":           OrgActionConfigureQueues,
	"DELETE /v1/apps/{slug}/queue-bindings/{id}":          OrgActionConfigureQueues,
	"GET /v1/apps/{slug}/queues/state":                    OrgActionReadQueues,
	"GET /v1/apps/{slug}/queues/peek":                     OrgActionOperateQueues,
	"GET /v1/apps/{slug}/queues/dead_letter":              OrgActionOperateQueues,
	"POST /v1/apps/{slug}/queues/send":                    OrgActionOperateQueues,
	"POST /v1/apps/{slug}/queues/receive":                 OrgActionOperateQueues,
	"POST /v1/apps/{slug}/queues/{id}/ack":                OrgActionOperateQueues,
	"POST /v1/apps/{slug}/queues/{queue}/leases":          OrgActionOperateQueues,
	"POST /v1/apps/{slug}/queues/{queue}/leases:ack":      OrgActionOperateQueues,
	"POST /v1/apps/{slug}/queues/{queue}/leases:nack":     OrgActionOperateQueues,
	"POST /v1/apps/{slug}/queues/{queue}/leases:extend":   OrgActionOperateQueues,
	"POST /v1/apps/{slug}/queues/dead_letter/{id}/replay": OrgActionReplayQueues,
}
