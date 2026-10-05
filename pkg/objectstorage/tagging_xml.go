package objectstorage

import (
	"bytes"
	"encoding/xml"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"io"
	"strings"
)

const taggingXMLNamespace = "http://s3.amazonaws.com/doc/2006-03-01/"

type taggingXMLText struct {
	XMLName xml.Name
	Value   string                       `xml:",chardata"`
	Extra   []struct{ XMLName xml.Name } `xml:",any"`
	Attrs   []xml.Attr                   `xml:",any,attr"`
}

func (v taggingXMLText) valid() bool {
	return len(v.Extra) == 0 && len(v.Attrs) == 0 && (v.XMLName.Space == "" || v.XMLName.Space == taggingXMLNamespace)
}

// ParseObjectTaggingXML validates the bounded S3 document before an SDK or
// ingress can interpret malformed, missing or repeated fields as empty tags.
func ParseObjectTaggingXML(body []byte) (map[string]string, error) {
	if int64(len(body)) > api.MaxObjectTaggingBodyBytes {
		return nil, ErrInvalid
	}
	var wire struct {
		XMLName xml.Name
		Attrs   []xml.Attr `xml:",any,attr"`
		Sets    []struct {
			XMLName xml.Name
			Tags    []struct {
				XMLName xml.Name
				Keys    []taggingXMLText             `xml:"Key"`
				Values  []taggingXMLText             `xml:"Value"`
				Extra   []struct{ XMLName xml.Name } `xml:",any"`
				Attrs   []xml.Attr                   `xml:",any,attr"`
				Text    string                       `xml:",chardata"`
			} `xml:"Tag"`
			Extra []struct{ XMLName xml.Name } `xml:",any"`
			Attrs []xml.Attr                   `xml:",any,attr"`
			Text  string                       `xml:",chardata"`
		} `xml:"TagSet"`
		Extra []struct{ XMLName xml.Name } `xml:",any"`
		Text  string                       `xml:",chardata"`
	}
	d := xml.NewDecoder(bytes.NewReader(body))
	if d.Decode(&wire) != nil || wire.XMLName.Local != "Tagging" || !validTaggingXMLNode(wire.XMLName, wire.Text, len(wire.Extra)) || len(wire.Sets) != 1 {
		return nil, ErrInvalid
	}
	var extra any
	if !errors.Is(d.Decode(&extra), io.EOF) {
		return nil, ErrInvalid
	}
	for _, attr := range wire.Attrs {
		if attr.Name.Local != "xmlns" && attr.Name.Space != "xmlns" {
			return nil, ErrInvalid
		}
	}
	set := wire.Sets[0]
	if len(set.Tags) > api.MaxObjectTags {
		return nil, ErrInvalid
	}
	if !validTaggingXMLNode(set.XMLName, set.Text, len(set.Extra)) || len(set.Attrs) != 0 {
		return nil, ErrInvalid
	}
	tags := make(map[string]string, len(set.Tags))
	for _, tag := range set.Tags {
		if !validTaggingXMLNode(tag.XMLName, tag.Text, len(tag.Extra)) || len(tag.Attrs) != 0 || len(tag.Keys) != 1 || len(tag.Values) != 1 || !tag.Keys[0].valid() || !tag.Values[0].valid() {
			return nil, ErrInvalid
		}
		key := tag.Keys[0].Value
		if _, exists := tags[key]; exists {
			return nil, ErrInvalid
		}
		tags[key] = tag.Values[0].Value
	}
	if err := ValidateObjectMetadata(ObjectMetadata{Tags: tags}); err != nil {
		return nil, err
	}
	return tags, nil
}

func validTaggingXMLNode(name xml.Name, text string, extra int) bool {
	return (name.Space == "" || name.Space == taggingXMLNamespace) && strings.TrimSpace(text) == "" && extra == 0
}
