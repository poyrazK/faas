package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// edgeRuleWAFFlags are the kind=waf flags shared by `edge-rules create` and
// `edge-rules update` (ADR-831).
type edgeRuleWAFFlags struct {
	mode             *string
	paranoiaLevel    *int
	anomalyThreshold *int
	excludeRules     *string
	inspectBodyBytes *int
}

func addEdgeRuleWAFFlags(fs *flag.FlagSet) edgeRuleWAFFlags {
	return edgeRuleWAFFlags{
		mode: fs.String("waf-mode", "",
			"kind=waf: observe (default), warn, or block; warn and block check headers and URL in-path at paranoia level 1"),
		paranoiaLevel: fs.Int("waf-paranoia-level", 0,
			fmt.Sprintf("kind=waf: OWASP CRS paranoia level 1..%d (default %d)", api.MaxEdgeWAFParanoiaLevel, api.EdgeWAFDefaultParanoiaLevel)),
		anomalyThreshold: fs.Int("waf-anomaly-threshold", 0,
			fmt.Sprintf("kind=waf: anomaly score that counts as a detection, 1..%d (default %d)", api.MaxEdgeWAFAnomalyThreshold, api.EdgeWAFDefaultAnomalyThreshold)),
		excludeRules: fs.String("waf-exclude-rules", "",
			"kind=waf: comma-separated CRS rule IDs to leave out of scoring, e.g. 942100,920350"),
		inspectBodyBytes: fs.Int("waf-inspect-body-bytes", 0,
			fmt.Sprintf("kind=waf: request body bytes to inspect, 1..%d (default %d)", api.MaxEdgeWAFInspectBodyBytes, api.EdgeWAFDefaultInspectBodyBytes)),
	}
}

func buildEdgeRuleWAFAction(in edgeRuleActionInputs) (json.RawMessage, error) {
	a := api.EdgeRuleWAFAction{
		Mode:             in.WAFMode,
		ParanoiaLevel:    in.WAFParanoiaLevel,
		AnomalyThreshold: in.WAFAnomalyThreshold,
		InspectBodyBytes: in.WAFInspectBodyBytes,
	}
	for _, field := range strings.Split(in.WAFExcludeRules, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		id, err := strconv.Atoi(field)
		if err != nil {
			return nil, fmt.Errorf("--waf-exclude-rules: %q is not a rule ID", field)
		}
		a.ExcludeRuleIDs = append(a.ExcludeRuleIDs, id)
	}
	if err := a.Validate(); err != nil {
		return nil, errToError(err)
	}
	return marshalAction(a)
}
