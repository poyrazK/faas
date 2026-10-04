package s3gateway

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type deleteXMLText struct {
	XMLName xml.Name
	Value   string                       `xml:",chardata"`
	Extra   []struct{ XMLName xml.Name } `xml:",any"`
	Attrs   []xml.Attr                   `xml:",any,attr"`
}

func (v deleteXMLText) valid() bool {
	return len(v.Extra) == 0 && len(v.Attrs) == 0 && (v.XMLName.Space == "" || v.XMLName.Space == s3XMLNamespace)
}

// Unknown fields, duplicate selectors and directory-bucket conditional fields
// must never degrade into an unconditional permanent delete.
func parseDeleteObjects(body []byte) (deleteObjectsRequest, error) {
	var wire struct {
		XMLName xml.Name
		Attrs   []xml.Attr `xml:",any,attr"`
		Objects []struct {
			XMLName xml.Name
			Text    string                       `xml:",chardata"`
			Keys    []deleteXMLText              `xml:"Key"`
			IDs     []deleteXMLText              `xml:"VersionId"`
			Extra   []struct{ XMLName xml.Name } `xml:",any"`
			Attrs   []xml.Attr                   `xml:",any,attr"`
		} `xml:"Object"`
		Quiet []deleteXMLText              `xml:"Quiet"`
		Extra []struct{ XMLName xml.Name } `xml:",any"`
		Text  string                       `xml:",chardata"`
	}
	result := deleteObjectsRequest{}
	d := xml.NewDecoder(bytes.NewReader(body))
	if d.Decode(&wire) != nil || wire.XMLName.Local != "Delete" || wire.XMLName.Space != "" && wire.XMLName.Space != s3XMLNamespace || strings.TrimSpace(wire.Text) != "" || len(wire.Extra) != 0 || len(wire.Quiet) > 1 || len(wire.Objects) == 0 || len(wire.Objects) > api.MaxObjectS3DeleteItems {
		return result, objectstorage.ErrInvalid
	}
	var extra any
	if !errors.Is(d.Decode(&extra), io.EOF) {
		return result, objectstorage.ErrInvalid
	}
	for _, attr := range wire.Attrs {
		if attr.Name.Local != "xmlns" && attr.Name.Space != "xmlns" {
			return result, objectstorage.ErrInvalid
		}
	}
	if len(wire.Quiet) == 1 {
		v := wire.Quiet[0]
		value := strings.TrimSpace(v.Value)
		if !v.valid() || value != "true" && value != "false" {
			return result, objectstorage.ErrInvalid
		}
		result.Quiet = value == "true"
	}
	for _, v := range wire.Objects {
		if len(v.Extra) != 0 || len(v.Attrs) != 0 || strings.TrimSpace(v.Text) != "" || v.XMLName.Space != "" && v.XMLName.Space != s3XMLNamespace || len(v.Keys) != 1 || !v.Keys[0].valid() || !objectstorage.ValidKey(v.Keys[0].Value) || len(v.IDs) > 1 {
			return result, objectstorage.ErrInvalid
		}
		target := deleteObjectTarget{Key: v.Keys[0].Value}
		if len(v.IDs) == 1 {
			if !v.IDs[0].valid() || !state.ValidObjectVersionID(v.IDs[0].Value) {
				return result, objectstorage.ErrInvalid
			}
			target.VersionID = v.IDs[0].Value
		}
		result.Objects = append(result.Objects, target)
	}
	return result, nil
}
