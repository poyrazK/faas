package objectstorage

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestLifecycleXMLRoundTrip(t *testing.T) {
	days, newer, marker := int32(7), int32(2), false
	date := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	rules := []api.ObjectLifecycleRule{
		{Status: "Enabled", Expiration: &api.ObjectLifecycleExpiration{Days: &days}},
		{ID: "date", Status: "Disabled", Filter: api.ObjectLifecycleFilter{Prefix: "目录 /+<&"}, Expiration: &api.ObjectLifecycleExpiration{Date: &date}},
		{ID: "tag", Status: "Enabled", Filter: api.ObjectLifecycleFilter{Tags: map[string]string{"kind": ""}}, NoncurrentVersionExpiration: &api.ObjectLifecycleNoncurrentExpiration{NoncurrentDays: 7, NewerNoncurrentVersions: &newer}},
		{ID: "and", Status: "Enabled", Filter: api.ObjectLifecycleFilter{Prefix: "archive/", Tags: map[string]string{"z": "β", "a": "<>&"}}, Expiration: &api.ObjectLifecycleExpiration{Days: &days}},
		{ID: "tags", Status: "Enabled", Filter: api.ObjectLifecycleFilter{Tags: map[string]string{"a": "1", "b": "2"}}, NoncurrentVersionExpiration: &api.ObjectLifecycleNoncurrentExpiration{NoncurrentDays: 8}},
		{ID: "marker", Status: "Enabled", Expiration: &api.ObjectLifecycleExpiration{ExpiredObjectDeleteMarker: &marker}},
		{ID: "multipart", Status: "Enabled", Filter: api.ObjectLifecycleFilter{Prefix: "tmp/"}, AbortIncompleteMultipartDays: &days},
	}
	want, err := api.NormalizeObjectLifecycleRules(rules)
	if err != nil {
		t.Fatal(err)
	}
	body, err := MarshalObjectLifecycleXML(rules)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseObjectLifecycleXML(body)
	if err != nil {
		t.Fatal(string(body), err)
	}
	// Empty and omitted maps have the same filter meaning.
	for i := range got {
		if len(got[i].Filter.Tags) == 0 {
			got[i].Filter.Tags = nil
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip: %#v != %#v", got, want)
	}
	again, err := MarshalObjectLifecycleXML(got)
	if err != nil || string(again) != string(body) {
		t.Fatal("noncanonical XML", err)
	}
}

func TestLifecycleXMLRejectsAmbiguousAndUnsupportedRules(t *testing.T) {
	wrap := func(rule string) string {
		return `<LifecycleConfiguration><Rule><Status>Enabled</Status>` + rule + `</Rule></LifecycleConfiguration>`
	}
	for _, tc := range []struct {
		name, body string
		want       error
	}{
		{"duplicate status", wrap(`<Status>Disabled</Status><Expiration><Days>1</Days></Expiration>`), ErrInvalid},
		{"duplicate ID", wrap(`<ID>a</ID><ID>b</ID><Expiration><Days>1</Days></Expiration>`), ErrInvalid},
		{"duplicate action", wrap(`<Expiration><Days>1</Days></Expiration><Expiration><Days>2</Days></Expiration>`), ErrInvalid},
		{"mixed expiration", wrap(`<Expiration><Days>1</Days><Date>2030-01-01T00:00:00Z</Date></Expiration>`), ErrInvalid},
		{"unknown rule", wrap(`<Expiration><Days>1</Days></Expiration><Typo/>`), ErrInvalid},
		{"transition", wrap(`<Transition><Days>1</Days><StorageClass>GLACIER</StorageClass></Transition>`), ErrUnsupported},
		{"size filter", wrap(`<Filter><ObjectSizeGreaterThan>1</ObjectSizeGreaterThan></Filter><Expiration><Days>1</Days></Expiration>`), ErrUnsupported},
		{"mixed prefix", wrap(`<Prefix>a</Prefix><Filter/><Expiration><Days>1</Days></Expiration>`), ErrInvalid},
		{"mixed filter", wrap(`<Filter><Prefix>a</Prefix><Tag><Key>k</Key><Value>v</Value></Tag></Filter><Expiration><Days>1</Days></Expiration>`), ErrInvalid},
		{"singleton and", wrap(`<Filter><And><Prefix>a</Prefix></And></Filter><Expiration><Days>1</Days></Expiration>`), ErrInvalid},
		{"duplicate tag", wrap(`<Filter><And><Tag><Key>k</Key><Value>a</Value></Tag><Tag><Key>k</Key><Value>b</Value></Tag></And></Filter><Expiration><Days>1</Days></Expiration>`), ErrInvalid},
		{"missing tag value", wrap(`<Filter><Tag><Key>k</Key></Tag></Filter><Expiration><Days>1</Days></Expiration>`), ErrInvalid},
		{"tag abort", wrap(`<Filter><Tag><Key>k</Key><Value/></Tag></Filter><AbortIncompleteMultipartUpload><DaysAfterInitiation>1</DaysAfterInitiation></AbortIncompleteMultipartUpload>`), ErrInvalid},
		{"retention without filter", wrap(`<NoncurrentVersionExpiration><NoncurrentDays>1</NoncurrentDays><NewerNoncurrentVersions>2</NewerNoncurrentVersions></NoncurrentVersionExpiration>`), ErrInvalid},
		{"zero", wrap(`<Expiration><Days>0</Days></Expiration>`), ErrInvalid},
		{"overflow", wrap(`<Expiration><Days>2147483648</Days></Expiration>`), ErrInvalid},
		{"boolean", wrap(`<Expiration><ExpiredObjectDeleteMarker>yes</ExpiredObjectDeleteMarker></Expiration>`), ErrInvalid},
		{"nonmidnight", wrap(`<Expiration><Date>2030-01-01T01:00:00Z</Date></Expiration>`), ErrInvalid},
		{"nested leaf", wrap(`<Expiration><Days><Days>1</Days></Days></Expiration>`), ErrInvalid},
		{"mixed text", wrap(`ignored<Expiration><Days>1</Days></Expiration>`), ErrInvalid},
		{"attribute", `<LifecycleConfiguration surprise="1"><Rule><Status>Enabled</Status><Expiration><Days>1</Days></Expiration></Rule></LifecycleConfiguration>`, ErrInvalid},
		{"namespace", `<LifecycleConfiguration xmlns="urn:foreign"><Rule><Status>Enabled</Status><Expiration><Days>1</Days></Expiration></Rule></LifecycleConfiguration>`, ErrInvalid},
		{"directive", `<!DOCTYPE LifecycleConfiguration>` + wrap(`<Expiration><Days>1</Days></Expiration>`), ErrInvalid},
		{"processing instruction", `<?application ignored?>` + wrap(`<Expiration><Days>1</Days></Expiration>`), ErrInvalid},
		{"trailing root", wrap(`<Expiration><Days>1</Days></Expiration>`) + `<other/>`, ErrInvalid},
		{"empty rules", `<LifecycleConfiguration/>`, ErrInvalid},
		{"too many rules", `<LifecycleConfiguration>` + strings.Repeat(`<Rule><Status>Enabled</Status><Expiration><Days>1</Days></Expiration></Rule>`, api.MaxObjectLifecycleRules+1) + `</LifecycleConfiguration>`, ErrInvalid},
		{"depth", `<LifecycleConfiguration>` + strings.Repeat(`<a>`, api.MaxObjectLifecycleXMLDepth) + strings.Repeat(`</a>`, api.MaxObjectLifecycleXMLDepth) + `</LifecycleConfiguration>`, ErrInvalid},
		{"nodes", `<LifecycleConfiguration>` + strings.Repeat(`<a/>`, api.MaxObjectLifecycleXMLNodes) + `</LifecycleConfiguration>`, ErrInvalid},
		{"body", strings.Repeat(" ", int(api.MaxObjectLifecycleBodyBytes)+1), ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseObjectLifecycleXML([]byte(tc.body)); !errors.Is(err, tc.want) {
				t.Fatal(err, tc.want)
			}
		})
	}
}

func FuzzLifecycleXMLCanonicalRoundTrip(f *testing.F) {
	f.Add([]byte(`<LifecycleConfiguration><Rule><Status>Enabled</Status><Prefix>tmp/</Prefix><Expiration><Days>1</Days></Expiration></Rule></LifecycleConfiguration>`))
	f.Add([]byte(`<?xml version="1.0"?><LifecycleConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Rule><Status>Enabled</Status><Filter/><Expiration><ExpiredObjectDeleteMarker>0</ExpiredObjectDeleteMarker></Expiration></Rule></LifecycleConfiguration>`))
	f.Fuzz(func(t *testing.T, body []byte) {
		rules, err := ParseObjectLifecycleXML(body)
		if err != nil {
			return
		}
		wire, err := MarshalObjectLifecycleXML(rules)
		if err != nil {
			t.Fatal(err)
		}
		round, err := ParseObjectLifecycleXML(wire)
		if err != nil {
			t.Fatal(err)
		}
		again, err := MarshalObjectLifecycleXML(round)
		if err != nil || string(wire) != string(again) {
			t.Fatal("canonical round trip failed", err)
		}
	})
}
