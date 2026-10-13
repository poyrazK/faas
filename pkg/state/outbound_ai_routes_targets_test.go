package state

// adr: 995 — openai routes accept Bedrock and Vertex targets with a model;
// a Vertex target needs one project and location in its path prefixes.

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestOpenAIRouteTargetsAcceptBedrockAndLocatedVertex(t *testing.T) {
	integrations := map[string]outboundAIRouteTarget{
		"bedrock":     {provider: api.OutboundAIProviderBedrock, pathPrefixes: []string{"/model"}},
		"vertex":      {provider: api.OutboundAIProviderVertex, pathPrefixes: []string{"/v1/projects/p/locations/global"}},
		"vertex-wide": {provider: api.OutboundAIProviderVertex, pathPrefixes: []string{"/v1"}},
	}
	for name, tc := range map[string]struct {
		format  string
		targets []OutboundAIRouteTarget
		want    bool
	}{
		"bedrock with model":       {api.OutboundAIRouteFormatOpenAI, []OutboundAIRouteTarget{{IntegrationID: "bedrock", Model: "amazon.nova-pro-v1:0"}}, true},
		"bedrock without model":    {api.OutboundAIRouteFormatOpenAI, []OutboundAIRouteTarget{{IntegrationID: "bedrock"}}, false},
		"located vertex":           {api.OutboundAIRouteFormatOpenAI, []OutboundAIRouteTarget{{IntegrationID: "vertex", Model: "claude-sonnet-4-5"}}, true},
		"vertex without location":  {api.OutboundAIRouteFormatOpenAI, []OutboundAIRouteTarget{{IntegrationID: "vertex-wide", Model: "gemini-2.5-pro"}}, false},
		"native vertex unaffected": {api.OutboundAIRouteFormatNative, []OutboundAIRouteTarget{{IntegrationID: "vertex-wide"}}, true},
	} {
		if got := outboundAIRouteTargetsAllowed(tc.format, tc.targets, integrations); got != tc.want {
			t.Errorf("%s: allowed = %v", name, got)
		}
	}
}
