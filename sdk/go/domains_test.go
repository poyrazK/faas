package faas

import (
	"encoding/json"
	"testing"
)

// ADR-520: custom-domain responses carry the DNS records to publish.
func TestCustomDomainResponseDecodesDNSRecords(t *testing.T) {
	body := `{"domain":"shop.example.com","app_id":"app-1","verified":false,
	  "dns_records":[{"type":"CNAME","name":"shop.example.com","value":"edge.gregale.dev","purpose":"routing"},
	                 {"type":"A","name":"shop.example.com","value":"203.0.113.10","purpose":"routing","alternative":true}]}`
	var got CustomDomainResponse
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	want := []DNSRecordInstruction{
		{Type: "CNAME", Name: "shop.example.com", Value: "edge.gregale.dev", Purpose: "routing"},
		{Type: "A", Name: "shop.example.com", Value: "203.0.113.10", Purpose: "routing", Alternative: true},
	}
	if len(got.DNSRecords) != len(want) {
		t.Fatalf("dns_records = %+v", got.DNSRecords)
	}
	for i := range want {
		if got.DNSRecords[i] != want[i] {
			t.Fatalf("dns_records[%d] = %+v, want %+v", i, got.DNSRecords[i], want[i])
		}
	}
}
