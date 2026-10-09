package main

import "testing"

func TestBuildEdgeRuleActionWAF(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      edgeRuleActionInputs
		want    string
		wantErr bool
	}{
		{name: "defaults", in: edgeRuleActionInputs{},
			want: `{"mode":"observe","paranoia_level":1,"anomaly_threshold":5,"inspect_body_bytes":8192}`},
		{name: "flags", in: edgeRuleActionInputs{WAFParanoiaLevel: 2, WAFAnomalyThreshold: 10, WAFExcludeRules: " 942100, 920350 ,", WAFInspectBodyBytes: 16384},
			want: `{"mode":"observe","paranoia_level":2,"anomaly_threshold":10,"exclude_rule_ids":[920350,942100],"inspect_body_bytes":16384}`},
		{name: "non-numeric exclusion", in: edgeRuleActionInputs{WAFExcludeRules: "942100,sqli"}, wantErr: true},
		{name: "paranoia level 3", in: edgeRuleActionInputs{WAFParanoiaLevel: 3}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := buildEdgeRuleAction("waf", tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && string(got) != tc.want {
				t.Errorf("action = %s, want %s", got, tc.want)
			}
		})
	}
}
