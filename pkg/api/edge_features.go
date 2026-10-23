package api

// Edge rule features released to accounts by platform flags
// (ADR-20261023113721), as EdgeRuleResponse.UnreleasedFeatures names them.
// While one is not released to the account, the gateway does not enforce it:
// a challenge rule is inert, a warn or block WAF rule only observes, and a
// penalty neither boxes nor refuses anyone.
const (
	EdgeFeatureChallenge   = "challenge"
	EdgeFeatureWAFBlocking = "waf_blocking"
	EdgeFeaturePenaltyBox  = "penalty_box"
)
