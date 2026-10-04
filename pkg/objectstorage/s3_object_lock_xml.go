package objectstorage

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/onebox-faas/faas/pkg/api"
)

const objectLockXMLNamespace = "http://s3.amazonaws.com/doc/2006-03-01/"

// Bound success and error responses before SDK deserialization. Unknown fields
// must not disappear into a parsed empty policy, and a 200 Error document must
// not be accepted as a successful policy mutation.
type objectLockResponseClient struct {
	base    aws.HTTPClient
	version string
	read    func([]byte) error
}

func (c objectLockResponseClient) Do(r *http.Request) (*http.Response, error) {
	response, err := c.base.Do(r)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, err
	}
	if response == nil || response.Body == nil {
		return nil, ErrUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, api.MaxObjectLockBodyBytes+1))
	_ = response.Body.Close()
	if err != nil || int64(len(body)) > api.MaxObjectLockBodyBytes {
		return nil, ErrUnavailable
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		if response.StatusCode != http.StatusOK || !validObjectLockResponseHeaders(response.Header, c.version) {
			return nil, ErrUnavailable
		}
		if c.read != nil {
			if err = c.read(body); err != nil {
				return nil, err
			}
		} else if len(bytes.TrimSpace(body)) != 0 {
			return nil, ErrUnavailable
		}
	} else if !validObjectLockErrorResponse(body) {
		// In particular, duplicate/unknown error fields must not manufacture
		// the special 404 code that establishes absent bucket protection.
		return nil, ErrUnavailable
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	return response, nil
}

func validObjectLockErrorResponse(body []byte) bool {
	v, err := objectLockXML(body, "Error", objectLockSchema("Error", "Code", "Message", "RequestId", "HostId", "Resource", "BucketName", "Key", "ArgumentName", "ArgumentValue", "Endpoint", "Region"))
	return err == nil && v["Error/Code"] != ""
}

func validObjectLockResponseHeaders(h http.Header, version string) bool {
	returned, marker := h.Get("X-Amz-Version-Id"), h.Get("X-Amz-Delete-Marker")
	return len(h.Values("X-Amz-Version-Id")) <= 1 && len(h.Values("X-Amz-Delete-Marker")) <= 1 &&
		(marker == "" || marker == "false") && (returned == "" || validNativeVersionID(returned) && returned == version)
}

type objectLockXMLFrame struct {
	path, text  string
	leaf, known bool
}

// Paths identify singular fields. An unknown, well-formed extension yields
// ErrUnsupported after the whole document is checked, keeping a known Enabled
// observation available. Duplicate fields or malformed documents are unsafe.
func objectLockXML(body []byte, root string, schema map[string]bool) (map[string]string, error) {
	values, seen := map[string]string{}, map[string]bool{}
	d := xml.NewDecoder(bytes.NewReader(body))
	var stack []objectLockXMLFrame
	namespace, nodes, unsupported, closed, declaration := "", 0, false, false, false
	for {
		token, err := d.Token()
		if errors.Is(err, io.EOF) {
			if !closed || len(stack) != 0 {
				return values, ErrUnavailable
			}
			if unsupported {
				return values, ErrUnsupported
			}
			return values, nil
		}
		if err != nil {
			return values, ErrUnavailable
		}
		switch token := token.(type) {
		case xml.StartElement:
			nodes++
			if closed || nodes > api.MaxObjectLockXMLElements || len(stack) >= api.MaxObjectLockXMLDepth {
				return values, ErrUnavailable
			}
			path := token.Name.Local
			if len(stack) == 0 {
				if path != root || token.Name.Space != "" && token.Name.Space != objectLockXMLNamespace {
					return values, ErrUnavailable
				}
				namespace = token.Name.Space
			} else {
				parent := stack[len(stack)-1]
				if parent.leaf {
					return values, ErrUnavailable
				}
				path = parent.path + "/" + path
			}
			if seen[path] {
				return values, ErrUnavailable
			}
			seen[path] = true
			leaf, known := schema[path]
			if !known || token.Name.Space != namespace {
				unsupported = true
			}
			if known {
				values[path] = ""
			}
			attributes := map[xml.Name]bool{}
			for _, attr := range token.Attr {
				if attributes[attr.Name] {
					return values, ErrUnavailable
				}
				attributes[attr.Name] = true
				namespaceAttribute := attr.Name.Space == "xmlns" || attr.Name.Space == "" && attr.Name.Local == "xmlns"
				if !namespaceAttribute || attr.Value != objectLockXMLNamespace && attr.Value != "" {
					unsupported = true
				}
			}
			stack = append(stack, objectLockXMLFrame{path: path, leaf: leaf, known: known})
		case xml.EndElement:
			if len(stack) == 0 {
				return values, ErrUnavailable
			}
			frame := stack[len(stack)-1]
			if frame.leaf {
				values[frame.path] = frame.text
			} else if frame.known && strings.Trim(frame.text, " \t\r\n") != "" {
				return values, ErrUnavailable
			}
			stack = stack[:len(stack)-1]
			closed = len(stack) == 0
		case xml.CharData:
			if len(stack) == 0 {
				if strings.Trim(string(token), " \t\r\n") != "" {
					return values, ErrUnavailable
				}
			} else {
				stack[len(stack)-1].text += string(token)
			}
		case xml.Directive:
			return values, ErrUnavailable
		case xml.ProcInst:
			if token.Target != "xml" || nodes != 0 || declaration {
				return values, ErrUnavailable
			}
			declaration = true
		}
	}
}

func objectLockSchema(root string, leaves ...string) map[string]bool {
	schema := map[string]bool{root: false}
	for _, leaf := range leaves {
		path := root
		parts := strings.Split(leaf, "/")
		for i, part := range parts {
			path += "/" + part
			schema[path] = i == len(parts)-1
		}
	}
	return schema
}

func parseBucketObjectLock(body []byte) (api.ObjectBucketObjectLockConfiguration, error) {
	const root = "ObjectLockConfiguration"
	v, err := objectLockXML(body, root, objectLockSchema(root, "ObjectLockEnabled", "Rule/DefaultRetention/Mode", "Rule/DefaultRetention/Days", "Rule/DefaultRetention/Years", "Rule/DefaultRetention/DefaultEventHold/Days", "Rule/DefaultRetention/DefaultEventHold/Years"))
	c := api.ObjectBucketObjectLockConfiguration{Enabled: v[root+"/ObjectLockEnabled"] == "Enabled"}
	if err != nil {
		return c, err
	}
	if !c.Enabled {
		return c, ErrUnavailable
	}
	const prefix = root + "/Rule/DefaultRetention/"
	// A present empty DefaultRetention is invalid, unlike an absent rule.
	if _, present := v[root+"/Rule"]; present {
		if _, configured := v[root+"/Rule/DefaultRetention"]; !configured {
			return c, ErrUnavailable
		}
	}
	if _, present := v[root+"/Rule/DefaultRetention"]; present {
		period, periodErr := parseObjectLockPeriod(v, prefix)
		hold, holdErr := parseObjectLockPeriod(v, prefix+"DefaultEventHold/")
		if periodErr != nil || holdErr != nil {
			return c, ErrUnavailable
		}
		if _, present := v[prefix+"DefaultEventHold"]; present && hold == nil {
			return c, ErrUnavailable
		}
		r := &api.ObjectLockDefaultRetention{Mode: v[prefix+"Mode"]}
		if period != nil {
			r.Days, r.Years = period.Days, period.Years
		}
		r.DefaultEventHold = hold
		c.DefaultRetention = r
		if !c.Enabled || !r.Valid() {
			return c, ErrUnavailable
		}
	}
	return c, nil
}

func parseObjectLockPeriod(v map[string]string, prefix string) (*api.ObjectRetentionPeriod, error) {
	var p api.ObjectRetentionPeriod
	for _, unit := range []string{"Days", "Years"} {
		if text, ok := v[prefix+unit]; ok {
			n, err := strconv.ParseInt(text, 10, 32)
			if err != nil || n <= 0 {
				return nil, ErrUnavailable
			}
			value := int32(n)
			if unit == "Days" {
				p.Days = &value
			} else {
				p.Years = &value
			}
		}
	}
	if p.Days == nil && p.Years == nil {
		return nil, nil
	}
	if !p.Valid() {
		return nil, ErrUnavailable
	}
	return &p, nil
}

func parseObjectRetention(body []byte) (api.ObjectVersionRetention, error) {
	const root = "Retention"
	v, err := objectLockXML(body, root, objectLockSchema(root, "Mode", "RetainUntilDate", "EventHold", "EventHoldDuration/Days", "EventHoldDuration/Years"))
	if err != nil {
		return api.ObjectVersionRetention{}, err
	}
	r := api.ObjectVersionRetention{Mode: v[root+"/Mode"], EventHold: v[root+"/EventHold"]}
	if mode, present := v[root+"/Mode"]; present && !api.ValidObjectLockMode(mode) {
		return r, ErrUnavailable
	}
	if hold, present := v[root+"/EventHold"]; present && hold != "ON" && hold != "OFF" {
		return r, ErrUnavailable
	}
	if date, ok := v[root+"/RetainUntilDate"]; ok {
		parsed, e := time.Parse(time.RFC3339Nano, date)
		if e != nil {
			return r, ErrUnavailable
		}
		parsed = parsed.UTC()
		r.RetainUntilDate = &parsed
	}
	if r.EventHold == "OFF" && r.RetainUntilDate == nil {
		return r, ErrUnavailable
	}
	r.EventHoldDuration, err = parseObjectLockPeriod(v, root+"/EventHoldDuration/")
	if _, present := v[root+"/EventHoldDuration"]; present && r.EventHoldDuration == nil {
		return r, ErrUnavailable
	}
	if err != nil || !r.Valid() {
		return r, ErrUnavailable
	}
	return r, nil
}

func parseObjectLegalHold(body []byte) (api.ObjectVersionLegalHold, error) {
	const root = "LegalHold"
	v, err := objectLockXML(body, root, objectLockSchema(root, "Status"))
	h := api.ObjectVersionLegalHold{Status: v[root+"/Status"]}
	if err != nil {
		return h, err
	}
	if !h.Valid() {
		return h, ErrUnavailable
	}
	return h, nil
}
