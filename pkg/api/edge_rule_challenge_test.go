package api

import "testing"

func TestEdgeRuleChallengeActionValidate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      EdgeRuleChallengeAction
		want    EdgeRuleChallengeAction
		wantErr bool
	}{
		{name: "defaults", want: EdgeRuleChallengeAction{Difficulty: EdgeChallengeDefaultDifficulty, PassTTLSeconds: EdgeChallengeDefaultPassTTLSeconds}},
		{name: "bounds kept", in: EdgeRuleChallengeAction{Difficulty: MaxEdgeChallengeDifficulty, PassTTLSeconds: MinEdgeChallengePassTTLSeconds},
			want: EdgeRuleChallengeAction{Difficulty: MaxEdgeChallengeDifficulty, PassTTLSeconds: MinEdgeChallengePassTTLSeconds}},
		{name: "difficulty too low", in: EdgeRuleChallengeAction{Difficulty: MinEdgeChallengeDifficulty - 1}, wantErr: true},
		{name: "difficulty too high", in: EdgeRuleChallengeAction{Difficulty: MaxEdgeChallengeDifficulty + 1}, wantErr: true},
		{name: "pass too short", in: EdgeRuleChallengeAction{PassTTLSeconds: MinEdgeChallengePassTTLSeconds - 1}, wantErr: true},
		{name: "pass too long", in: EdgeRuleChallengeAction{PassTTLSeconds: MaxEdgeChallengePassTTLSeconds + 1}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := tc.in
			err := a.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			if err == nil && a != tc.want {
				t.Errorf("effective action = %+v, want %+v", a, tc.want)
			}
		})
	}
	var nilAction *EdgeRuleChallengeAction
	if nilAction.Validate() == nil {
		t.Error("nil action accepted")
	}
}

func TestErrChallengeRequired(t *testing.T) {
	p := ErrChallengeRequired("rule-1")
	if p.Status != 403 || p.Code != CodeChallengeRequired || StatusForCode(CodeChallengeRequired) != 403 {
		t.Fatalf("problem = %+v, status for code %d", p, StatusForCode(CodeChallengeRequired))
	}
}

func TestChallengeLimitsByPlan(t *testing.T) {
	for plan, want := range map[Plan]int{PlanFree: 0, PlanHobby: 3, PlanPro: 10, PlanScale: 25} {
		if got := MustLimitsFor(plan).EdgeRulesChallengePerApp; got != want {
			t.Errorf("%s: EdgeRulesChallengePerApp = %d, want %d", plan, got, want)
		}
	}
	if !EdgeRuleKindUsesProtectivePathMatch("challenge") {
		t.Error("challenge must match paths protectively")
	}
}
