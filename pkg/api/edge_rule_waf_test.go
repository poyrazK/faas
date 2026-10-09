package api

import (
	"slices"
	"testing"
)

func TestEdgeRuleWAFActionValidate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      EdgeRuleWAFAction
		wantErr bool
		want    EdgeRuleWAFAction
	}{
		{name: "defaults", in: EdgeRuleWAFAction{}, want: EdgeRuleWAFAction{Mode: "observe", ParanoiaLevel: 1, AnomalyThreshold: 5, InspectBodyBytes: EdgeWAFDefaultInspectBodyBytes}},
		{name: "explicit", in: EdgeRuleWAFAction{Mode: "observe", ParanoiaLevel: 2, AnomalyThreshold: 10, ExcludeRuleIDs: []int{942100, 920350, 942100}, InspectBodyBytes: MaxEdgeWAFInspectBodyBytes},
			want: EdgeRuleWAFAction{Mode: "observe", ParanoiaLevel: 2, AnomalyThreshold: 10, ExcludeRuleIDs: []int{920350, 942100}, InspectBodyBytes: MaxEdgeWAFInspectBodyBytes}},
		{name: "body cap too large", in: EdgeRuleWAFAction{InspectBodyBytes: MaxEdgeWAFInspectBodyBytes + 1}, wantErr: true},
		{name: "negative body cap", in: EdgeRuleWAFAction{InspectBodyBytes: -1}, wantErr: true},
		{name: "block mode not available", in: EdgeRuleWAFAction{Mode: "block"}, wantErr: true},
		{name: "paranoia level 3", in: EdgeRuleWAFAction{ParanoiaLevel: 3}, wantErr: true},
		{name: "negative threshold", in: EdgeRuleWAFAction{AnomalyThreshold: -1}, wantErr: true},
		{name: "threshold too high", in: EdgeRuleWAFAction{AnomalyThreshold: MaxEdgeWAFAnomalyThreshold + 1}, wantErr: true},
		{name: "non-CRS rule ID", in: EdgeRuleWAFAction{ExcludeRuleIDs: []int{12345}}, wantErr: true},
		{name: "too many exclusions", in: EdgeRuleWAFAction{ExcludeRuleIDs: make([]int, MaxEdgeWAFExcludedRules+1)}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.in
			prob := got.Validate()
			if (prob != nil) != tc.wantErr {
				t.Fatalf("Validate() problem = %v, wantErr %v", prob, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if got.Mode != tc.want.Mode || got.ParanoiaLevel != tc.want.ParanoiaLevel ||
				got.AnomalyThreshold != tc.want.AnomalyThreshold || got.InspectBodyBytes != tc.want.InspectBodyBytes || !slices.Equal(got.ExcludeRuleIDs, tc.want.ExcludeRuleIDs) {
				t.Errorf("Validate() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestEdgeRulesWAFPerAppIsProAndAbove(t *testing.T) {
	for plan, want := range map[Plan]int{PlanFree: 0, PlanHobby: 0, PlanPro: 5, PlanScale: 20} {
		limits, ok := LimitsFor(plan)
		if !ok {
			t.Fatalf("no limits for %s", plan)
		}
		if limits.EdgeRulesWAFPerApp != want {
			t.Errorf("%s EdgeRulesWAFPerApp = %d, want %d", plan, limits.EdgeRulesWAFPerApp, want)
		}
	}
}
