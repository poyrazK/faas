package main

import (
	"encoding/json"
	"flag"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// edgeRuleChallengeFlags are the kind=challenge flags shared by `edge-rules
// create` and `edge-rules update` (ADR-831 amendment 7).
type edgeRuleChallengeFlags struct {
	difficulty     *int
	passTTLSeconds *int
	maxDifficulty  *int
}

func addEdgeRuleChallengeFlags(fs *flag.FlagSet) edgeRuleChallengeFlags {
	return edgeRuleChallengeFlags{
		difficulty: fs.Int("challenge-difficulty", 0,
			fmt.Sprintf("kind=challenge: proof-of-work difficulty in leading zero bits, %d..%d (default %d); each step doubles the browser's work",
				api.MinEdgeChallengeDifficulty, api.MaxEdgeChallengeDifficulty, api.EdgeChallengeDefaultDifficulty)),
		passTTLSeconds: fs.Int("challenge-pass-ttl", 0,
			fmt.Sprintf("kind=challenge: seconds a solved challenge lets the browser through, %d..%d (default %d)",
				api.MinEdgeChallengePassTTLSeconds, api.MaxEdgeChallengePassTTLSeconds, api.EdgeChallengeDefaultPassTTLSeconds)),
		maxDifficulty: fs.Int("challenge-max-difficulty", 0,
			fmt.Sprintf("kind=challenge: raise the difficulty up to this many bits, at most %d, while the rule serves far more challenge pages than usual (default: fixed)",
				api.MaxEdgeChallengeDifficulty)),
	}
}

func buildEdgeRuleChallengeAction(in edgeRuleActionInputs) (json.RawMessage, error) {
	a := api.EdgeRuleChallengeAction{
		Difficulty:     in.ChallengeDifficulty,
		PassTTLSeconds: in.ChallengePassTTLSeconds,
		MaxDifficulty:  in.ChallengeMaxDifficulty,
	}
	if err := a.Validate(); err != nil {
		return nil, errToError(err)
	}
	return marshalAction(a)
}
