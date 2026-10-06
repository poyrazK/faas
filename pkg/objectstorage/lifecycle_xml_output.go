package objectstorage

import (
	"encoding/xml"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type lifecycleXMLConfiguration struct {
	XMLName xml.Name           `xml:"LifecycleConfiguration"`
	XMLNS   string             `xml:"xmlns,attr"`
	Rules   []lifecycleXMLRule `xml:"Rule"`
}
type lifecycleXMLRule struct {
	ID         string                  `xml:"ID"`
	Filter     lifecycleXMLFilter      `xml:"Filter"`
	Status     string                  `xml:"Status"`
	Expiration *lifecycleXMLExpiration `xml:"Expiration,omitempty"`
	Noncurrent *lifecycleXMLNoncurrent `xml:"NoncurrentVersionExpiration,omitempty"`
	Abort      *lifecycleXMLAbort      `xml:"AbortIncompleteMultipartUpload,omitempty"`
}
type lifecycleXMLFilter struct {
	Prefix *string          `xml:"Prefix,omitempty"`
	Tag    *taggingTag      `xml:"Tag,omitempty"`
	And    *lifecycleXMLAnd `xml:"And,omitempty"`
}
type taggingTag struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}
type lifecycleXMLAnd struct {
	Prefix *string      `xml:"Prefix,omitempty"`
	Tags   []taggingTag `xml:"Tag"`
}
type lifecycleXMLExpiration struct {
	Days   *int32  `xml:"Days,omitempty"`
	Date   *string `xml:"Date,omitempty"`
	Marker *bool   `xml:"ExpiredObjectDeleteMarker,omitempty"`
}
type lifecycleXMLNoncurrent struct {
	Days  int32  `xml:"NoncurrentDays"`
	Newer *int32 `xml:"NewerNoncurrentVersions,omitempty"`
}
type lifecycleXMLAbort struct {
	Days int32 `xml:"DaysAfterInitiation"`
}

func MarshalObjectLifecycleXML(rules []api.ObjectLifecycleRule) ([]byte, error) {
	valid, err := api.NormalizeObjectLifecycleRules(rules)
	if err != nil || len(valid) == 0 {
		return nil, ErrInvalid
	}
	wire := lifecycleXMLConfiguration{XMLNS: taggingXMLNamespace, Rules: make([]lifecycleXMLRule, 0, len(valid))}
	for _, r := range valid {
		out := lifecycleXMLRule{ID: r.ID, Status: r.Status, Filter: lifecycleXMLFilterFor(r.Filter)}
		if r.Expiration != nil {
			out.Expiration = &lifecycleXMLExpiration{Days: r.Expiration.Days, Marker: r.Expiration.ExpiredObjectDeleteMarker}
			if r.Expiration.Date != nil {
				v := r.Expiration.Date.UTC().Format(time.RFC3339)
				out.Expiration.Date = &v
			}
		}
		if r.NoncurrentVersionExpiration != nil {
			out.Noncurrent = &lifecycleXMLNoncurrent{Days: r.NoncurrentVersionExpiration.NoncurrentDays, Newer: r.NoncurrentVersionExpiration.NewerNoncurrentVersions}
		}
		if r.AbortIncompleteMultipartDays != nil {
			out.Abort = &lifecycleXMLAbort{Days: *r.AbortIncompleteMultipartDays}
		}
		wire.Rules = append(wire.Rules, out)
	}
	return xml.Marshal(wire)
}
func lifecycleXMLFilterFor(f api.ObjectLifecycleFilter) lifecycleXMLFilter {
	keys := make([]string, 0, len(f.Tags))
	for key := range f.Tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	tags := make([]taggingTag, 0, len(keys))
	for _, key := range keys {
		tags = append(tags, taggingTag{Key: key, Value: f.Tags[key]})
	}
	if len(tags) == 0 {
		return lifecycleXMLFilter{Prefix: &f.Prefix}
	}
	if len(tags) == 1 && f.Prefix == "" {
		return lifecycleXMLFilter{Tag: &tags[0]}
	}
	and := &lifecycleXMLAnd{Tags: tags}
	if f.Prefix != "" {
		and.Prefix = &f.Prefix
	}
	return lifecycleXMLFilter{And: and}
}
