package api

// Edge rule features released to accounts by platform flags
// (ADR-20261023113721), as EdgeRuleResponse.UnreleasedFeatures names them.
// While one is not released to the account, the gateway does not enforce it:
// a challenge rule is inert, a warn or block WAF rule only observes, a
// penalty neither boxes nor refuses anyone, an AI crawler condition never
// matches, and attack mode challenges nobody.
const (
	EdgeFeatureChallenge   = "challenge"
	EdgeFeatureWAFBlocking = "waf_blocking"
	EdgeFeaturePenaltyBox  = "penalty_box"
	EdgeFeatureAICrawlers  = "ai_crawlers"
	EdgeFeatureAttackMode  = "attack_mode"
)
