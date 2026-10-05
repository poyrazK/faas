package main

import (
	"bytes"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPrintDomainDNSRecords(t *testing.T) {
	for _, tc := range []struct {
		name string
		resp api.CustomDomainResponse
		want string
	}{
		{
			name: "older server sends only the challenge",
			resp: api.CustomDomainResponse{Domain: "shop.example.com", ChallengeToken: "tok"},
			want: "  _faas-verify.shop.example.com  TXT  tok\n",
		},
		{
			name: "records with an apex alternative",
			resp: api.CustomDomainResponse{Domain: "shop.example.com", DNSRecords: []api.DNSRecordInstruction{
				{Type: "TXT", Name: "_faas-verify.shop.example.com", Value: "tok", Purpose: api.DNSRecordPurposeVerification},
				{Type: "CNAME", Name: "shop.example.com", Value: "edge.gregale.dev", Purpose: api.DNSRecordPurposeRouting},
				{Type: "A", Name: "shop.example.com", Value: "203.0.113.10", Purpose: api.DNSRecordPurposeRouting, Alternative: true},
			}},
			want: "  _faas-verify.shop.example.com  TXT  tok\n" +
				"  shop.example.com  CNAME  edge.gregale.dev\n" +
				"  shop.example.com  A  203.0.113.10  (at a zone apex, instead of the CNAME)\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			printDomainDNSRecords(&buf, tc.resp)
			if buf.String() != tc.want {
				t.Fatalf("output:\n%s\nwant:\n%s", buf.String(), tc.want)
			}
		})
	}
}
