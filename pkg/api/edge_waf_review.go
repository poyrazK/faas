package api

import (
	"slices"
	"strconv"
	"time"
)

// ADR-831 amendment 13: the WAF false-positive review. An app's sampled WAF
// detections (amendment 5) grouped by edge rule, CRS rule and path, each
// group assessed, so a customer (and an operator running the W4 review of
// ADR-20261024082000) can tell a false positive from an attack and exclude
// the rule in one step.

// Assessments of an EdgeWAFReviewGroup.
const (
	// EdgeWAFReviewLikelyFalsePositive: a documented false positive, or
	// many unrelated clients flagged on one path by one CRS rule.
	EdgeWAFReviewLikelyFalsePositive = "likely_false_positive"
	// EdgeWAFReviewLikelyAttack: most detections came from clients flagged
	// on many paths, which is how scanners behave.
	EdgeWAFReviewLikelyAttack = "likely_attack"
	// EdgeWAFReviewUndecided: too few clients to tell; read the examples.
	EdgeWAFReviewUndecided = "review"
	// EdgeWAFReviewExcluded: the rule already excludes this CRS rule; the
	// detections came before the exclusion.
	EdgeWAFReviewExcluded = "excluded"
)

// EdgeWAFReviewResponse is GET /v1/apps/{slug}/edge-rules/waf-review.
// Scanned counts the detection events read; Truncated reports that the
// window held more than a review reads (EdgeWAFReviewMaxEvents per outcome,
// newest first), so its oldest detections were left out. Detections are
// sampled (10 per rule per minute per gateway), so counts are lower bounds.
type EdgeWAFReviewResponse struct {
	Since     time.Time            `json:"since"`
	Scanned   int                  `json:"scanned"`
	Truncated bool                 `json:"truncated"`
	Groups    []EdgeWAFReviewGroup `json:"groups"`
}

// EdgeWAFReviewGroup is one edge rule's detections by one CRS rule on one
// path. Path is the request path with id-like segments (numbers, UUIDs,
// long hex or tokens) read as {id}, so /orders/17 and /orders/18 group
// together. Clients counts distinct client IPs; TopClientPercent is the
// share of the group's detections from its busiest client, and
// ScannerPercent the share from clients flagged on
// EdgeWAFReviewScannerPaths or more paths in the window. RequestIDs are the
// newest examples. KnownFalsePositive names the documented legitimate
// traffic this CRS rule flags (docs/security.md), and Excluded reports that
// the rule excludes it now.
type EdgeWAFReviewGroup struct {
	RuleID             string    `json:"rule_id"`
	RuleName           string    `json:"rule_name,omitempty"`
	RuleMode           string    `json:"rule_mode,omitempty"`
	CRSRuleID          int       `json:"crs_rule_id"`
	Category           string    `json:"category"`
	Path               string    `json:"path"`
	Events             int       `json:"events"`
	Detected           int       `json:"detected"`
	Warned             int       `json:"warned"`
	Blocked            int       `json:"blocked"`
	Clients            int       `json:"clients"`
	TopClientPercent   int       `json:"top_client_percent"`
	ScannerPercent     int       `json:"scanner_percent"`
	FirstSeen          time.Time `json:"first_seen"`
	LastSeen           time.Time `json:"last_seen"`
	RequestIDs         []string  `json:"request_ids"`
	KnownFalsePositive string    `json:"known_false_positive,omitempty"`
	Excluded           bool      `json:"excluded"`
	Assessment         string    `json:"assessment"`
	Reason             string    `json:"reason"`
}

// EdgeWAFExclusionsRequest is POST /v1/edge-rules/{id}/waf-exclusions: CRS
// rule IDs to add to and remove from a kind=waf rule's exclude_rule_ids.
// The rest of the rule's action, and its penalty, are kept.
type EdgeWAFExclusionsRequest struct {
	Add    []int `json:"add,omitempty"`
	Remove []int `json:"remove,omitempty"`
}

// Validate checks every ID names a CRS rule and that the request changes
// something.
func (r EdgeWAFExclusionsRequest) Validate() *Problem {
	if len(r.Add) == 0 && len(r.Remove) == 0 {
		return ErrValidation("add or remove at least one CRS rule ID")
	}
	for _, id := range r.Add {
		if slices.Contains(r.Remove, id) {
			return ErrValidation("CRS rule " + strconv.Itoa(id) + " is in both add and remove")
		}
	}
	for _, ids := range [][]int{r.Add, r.Remove} {
		if len(ids) > MaxEdgeWAFExcludedRules {
			return ErrValidation("at most " + strconv.Itoa(MaxEdgeWAFExcludedRules) + " CRS rule IDs per request")
		}
		for _, id := range ids {
			if id < EdgeWAFCRSRuleIDMin || id > EdgeWAFCRSRuleIDMax {
				return ErrValidation(strconv.Itoa(id) + " is not a CRS rule ID (" +
					strconv.Itoa(EdgeWAFCRSRuleIDMin) + ".." + strconv.Itoa(EdgeWAFCRSRuleIDMax) + ")")
			}
		}
	}
	return nil
}

// EdgeWAFRuleCategory is the attack category of a CRS rule, from the rule
// file its ID falls in, in the vocabulary of gateway_waf_detections_total.
func EdgeWAFRuleCategory(id int) string {
	switch id / 1000 {
	case 911, 920, 921:
		return "protocol"
	case 913:
		return "scanner"
	case 922:
		return "multipart"
	case 930:
		return "lfi"
	case 931:
		return "rfi"
	case 932:
		return "rce"
	case 933:
		return "php"
	case 934:
		return "generic"
	case 941:
		return "xss"
	case 942:
		return "sqli"
	case 943:
		return "session_fixation"
	case 944:
		return "java"
	}
	return "other"
}

// EdgeWAFKnownFalsePositive names the legitimate traffic a CRS rule is known
// to flag at paranoia level 1 (ADR-831 amendment 6; the table in
// docs/security.md), or "". pkg/edgewaf's benign corpus pins exactly these
// rules (TestKnownFalsePositivesMatchBenignCorpus).
func EdgeWAFKnownFalsePositive(id int) string {
	switch id {
	case 942190:
		return "GraphQL query in the URL"
	case 932235, 942290:
		return "GraphQL request body"
	case 942350:
		return "prose with SQL keywords after a semicolon"
	case 942100:
		return "content that is SQL, such as a docs search or a snippet"
	case 941160:
		return "content that is HTML, such as a CMS editor"
	}
	return ""
}
