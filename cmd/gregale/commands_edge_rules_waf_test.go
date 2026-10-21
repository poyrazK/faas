package main_test

import (
	"github.com/onebox-faas/faas/cmd/gregale"
	"testing"
)

func TestBuildEdgeRuleActionWAF(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      main.EdgeRuleActionInputs
		want    string
		wantErr bool
	}{
		{name: "defaults", in: main.EdgeRuleActionInputs{},
			want: `{"mode":"observe","paranoia_level":1,"anomaly_threshold":5,"inspect_body_bytes":8192}`},
		{name: "flags", in: main.EdgeRuleActionInputs{WAFParanoiaLevel: 2, WAFAnomalyThreshold: 10, WAFExcludeRules: " 942100, 920350 ,", WAFInspectBodyBytes: 16384},
			want: `{"mode":"observe","paranoia_level":2,"anomaly_threshold":10,"exclude_rule_ids":[920350,942100],"inspect_body_bytes":16384}`},
		{name: "non-numeric exclusion", in: main.EdgeRuleActionInputs{WAFExcludeRules: "942100,sqli"}, wantErr: true},
		{name: "paranoia level 3", in: main.EdgeRuleActionInputs{WAFParanoiaLevel: 3}, wantErr: true},
		{name: "block mode", in: main.EdgeRuleActionInputs{WAFMode: "block"},
			want: `{"mode":"block","paranoia_level":1,"anomaly_threshold":5,"inspect_body_bytes":8192}`},
		{name: "block at paranoia level 2", in: main.EdgeRuleActionInputs{WAFMode: "block", WAFParanoiaLevel: 2}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := main.BuildEdgeRuleAction("waf", tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && string(got) != tc.want {
				t.Errorf("action = %s, want %s", got, tc.want)
			}
		})
	}
}
