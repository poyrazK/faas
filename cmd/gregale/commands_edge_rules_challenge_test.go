package main

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBuildEdgeRuleChallengeAction(t *testing.T) {
	raw, err := buildEdgeRuleChallengeAction(edgeRuleActionInputs{})
	if err != nil {
		t.Fatal(err)
	}
	var got api.EdgeRuleChallengeAction
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Difficulty != api.EdgeChallengeDefaultDifficulty || got.PassTTLSeconds != api.EdgeChallengeDefaultPassTTLSeconds {
		t.Errorf("defaults = %+v", got)
	}

	raw, err = buildEdgeRuleChallengeAction(edgeRuleActionInputs{ChallengeDifficulty: 20, ChallengePassTTLSeconds: 600})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &got); err != nil || got.Difficulty != 20 || got.PassTTLSeconds != 600 {
		t.Errorf("explicit = %+v (err %v)", got, err)
	}

	if _, err := buildEdgeRuleChallengeAction(edgeRuleActionInputs{ChallengeDifficulty: 40}); err == nil {
		t.Error("difficulty 40 accepted")
	}
	found := false
	for _, k := range edgeRuleKindVocab {
		found = found || k == "challenge"
	}
	if !found {
		t.Error("challenge missing from the CLI kind vocabulary")
	}
}
