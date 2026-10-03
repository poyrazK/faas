package objectstorage

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type lifecycleXMLNode struct {
	name     string
	text     strings.Builder
	children []*lifecycleXMLNode
}

// Decode into a bounded tree first so unknown and repeated fields cannot be
// dropped by encoding/xml's permissive struct decoder. No directives or PIs
// other than an initial XML declaration are interpreted.
func readLifecycleXML(body []byte) (*lifecycleXMLNode, error) {
	return readBoundedObjectXML(body, api.MaxObjectLifecycleBodyBytes, api.MaxObjectLifecycleXMLDepth, api.MaxObjectLifecycleXMLNodes)
}

func readBoundedObjectXML(body []byte, maxBytes int64, maxDepth, maxNodes int) (*lifecycleXMLNode, error) {
	if int64(len(body)) > maxBytes {
		return nil, ErrInvalid
	}
	d := xml.NewDecoder(bytes.NewReader(body))
	var root *lifecycleXMLNode
	stack := []*lifecycleXMLNode{}
	nodes, declarations := 0, 0
	for {
		token, err := d.Token()
		if errors.Is(err, io.EOF) {
			if root == nil || len(stack) != 0 {
				return nil, ErrInvalid
			}
			return root, nil
		}
		if err != nil {
			return nil, ErrInvalid
		}
		switch v := token.(type) {
		case xml.StartElement:
			nodes++
			if nodes > maxNodes || len(stack) >= maxDepth || v.Name.Space != "" && v.Name.Space != taggingXMLNamespace {
				return nil, ErrInvalid
			}
			for _, a := range v.Attr {
				if a.Name.Local != "xmlns" && a.Name.Space != "xmlns" {
					return nil, ErrInvalid
				}
			}
			n := &lifecycleXMLNode{name: v.Name.Local}
			if len(stack) == 0 {
				if root != nil {
					return nil, ErrInvalid
				}
				root = n
			} else {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, n)
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, ErrInvalid
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 {
				if strings.TrimSpace(string(v)) != "" {
					return nil, ErrInvalid
				}
			} else {
				_, _ = stack[len(stack)-1].text.Write(v)
			}
		case xml.ProcInst:
			declarations++
			if v.Target != "xml" || root != nil || declarations != 1 {
				return nil, ErrInvalid
			}
		case xml.Directive:
			return nil, ErrInvalid
		}
	}
}

func (n *lifecycleXMLNode) fields(allowed ...string) error {
	if strings.TrimSpace(n.text.String()) != "" {
		return ErrInvalid
	}
	for _, c := range n.children {
		found := false
		for _, name := range allowed {
			found = found || c.name == name
		}
		if found {
			continue
		}
		switch c.name {
		case "Transition", "NoncurrentVersionTransition", "ObjectSizeGreaterThan", "ObjectSizeLessThan":
			return ErrUnsupported
		default:
			return ErrInvalid
		}
	}
	return nil
}
func (n *lifecycleXMLNode) one(name string, required bool) (*lifecycleXMLNode, error) {
	var result *lifecycleXMLNode
	for _, c := range n.children {
		if c.name == name {
			if result != nil {
				return nil, ErrInvalid
			}
			result = c
		}
	}
	if required && result == nil {
		return nil, ErrInvalid
	}
	return result, nil
}
func (n *lifecycleXMLNode) scalar() (string, error) {
	if len(n.children) != 0 {
		return "", ErrInvalid
	}
	return n.text.String(), nil
}
func (n *lifecycleXMLNode) value(name string, required bool) (string, error) {
	v, err := n.one(name, required)
	if err != nil || v == nil {
		return "", err
	}
	return v.scalar()
}
func (n *lifecycleXMLNode) days(name string, required bool) (*int32, error) {
	v, err := n.one(name, required)
	if err != nil || v == nil {
		return nil, err
	}
	text, err := v.scalar()
	if err != nil {
		return nil, err
	}
	days, err := strconv.ParseInt(strings.TrimSpace(text), 10, 32)
	if err != nil || days < 1 {
		return nil, ErrInvalid
	}
	out := int32(days)
	return &out, nil
}

func ParseObjectLifecycleXML(body []byte) ([]api.ObjectLifecycleRule, error) {
	root, err := readLifecycleXML(body)
	if err != nil {
		return nil, err
	}
	if root.name != "LifecycleConfiguration" || len(root.children) < 1 || len(root.children) > api.MaxObjectLifecycleRules {
		return nil, ErrInvalid
	}
	if err = root.fields("Rule"); err != nil {
		return nil, err
	}
	rules := make([]api.ObjectLifecycleRule, 0, len(root.children))
	for _, node := range root.children {
		rule, err := parseLifecycleXMLRule(node)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	valid, err := api.NormalizeObjectLifecycleRules(rules)
	if err != nil {
		return nil, ErrInvalid
	}
	return valid, nil
}

func parseLifecycleXMLRule(n *lifecycleXMLNode) (api.ObjectLifecycleRule, error) {
	r := api.ObjectLifecycleRule{}
	if err := n.fields("ID", "Status", "Filter", "Prefix", "Expiration", "NoncurrentVersionExpiration", "AbortIncompleteMultipartUpload"); err != nil {
		return r, err
	}
	var err error
	if r.ID, err = n.value("ID", false); err != nil {
		return r, err
	}
	if r.Status, err = n.value("Status", true); err != nil {
		return r, err
	}
	filter, err := n.one("Filter", false)
	if err != nil {
		return r, err
	}
	prefix, err := n.one("Prefix", false)
	if err != nil || filter != nil && prefix != nil {
		return r, ErrInvalid
	}
	if filter != nil {
		if r.Filter, err = parseLifecycleXMLFilter(filter); err != nil {
			return r, err
		}
	} else if prefix != nil {
		if r.Filter.Prefix, err = prefix.scalar(); err != nil {
			return r, err
		}
	}
	expiration, err := n.one("Expiration", false)
	if err != nil {
		return r, err
	}
	if expiration != nil {
		if r.Expiration, err = parseLifecycleXMLExpiration(expiration); err != nil {
			return r, err
		}
	}
	noncurrent, err := n.one("NoncurrentVersionExpiration", false)
	if err != nil {
		return r, err
	}
	if noncurrent != nil {
		if err = noncurrent.fields("NoncurrentDays", "NewerNoncurrentVersions"); err != nil {
			return r, err
		}
		days, e := noncurrent.days("NoncurrentDays", true)
		if e != nil {
			return r, e
		}
		newer, e := noncurrent.days("NewerNoncurrentVersions", false)
		if e != nil {
			return r, e
		}
		if newer != nil && filter == nil {
			return r, ErrInvalid
		}
		r.NoncurrentVersionExpiration = &api.ObjectLifecycleNoncurrentExpiration{NoncurrentDays: *days, NewerNoncurrentVersions: newer}
	}
	abort, err := n.one("AbortIncompleteMultipartUpload", false)
	if err != nil {
		return r, err
	}
	if abort != nil {
		if err = abort.fields("DaysAfterInitiation"); err != nil {
			return r, err
		}
		if r.AbortIncompleteMultipartDays, err = abort.days("DaysAfterInitiation", true); err != nil {
			return r, err
		}
	}
	return r, nil
}

func parseLifecycleXMLFilter(n *lifecycleXMLNode) (api.ObjectLifecycleFilter, error) {
	f := api.ObjectLifecycleFilter{Tags: map[string]string{}}
	if err := n.fields("Prefix", "Tag", "And"); err != nil {
		return f, err
	}
	if len(n.children) == 0 {
		return f, nil
	}
	if len(n.children) != 1 {
		return f, ErrInvalid
	}
	and := n.children[0].name == "And"
	if and {
		n = n.children[0]
		if err := n.fields("Prefix", "Tag"); err != nil {
			return f, err
		}
		if len(n.children) < 2 {
			return f, ErrInvalid
		}
	}
	var err error
	if f.Prefix, err = n.value("Prefix", false); err != nil {
		return f, err
	}
	for _, node := range n.children {
		if node.name != "Tag" {
			continue
		}
		if err = node.fields("Key", "Value"); err != nil {
			return f, err
		}
		key, e := node.value("Key", true)
		if e != nil {
			return f, e
		}
		value, e := node.value("Value", true)
		if e != nil {
			return f, e
		}
		if _, exists := f.Tags[key]; exists {
			return f, ErrInvalid
		}
		f.Tags[key] = value
	}
	return f, nil
}

func parseLifecycleXMLExpiration(n *lifecycleXMLNode) (*api.ObjectLifecycleExpiration, error) {
	if err := n.fields("Days", "Date", "ExpiredObjectDeleteMarker"); err != nil {
		return nil, err
	}
	if len(n.children) != 1 {
		return nil, ErrInvalid
	}
	e := &api.ObjectLifecycleExpiration{}
	var err error
	switch n.children[0].name {
	case "Days":
		e.Days, err = n.days("Days", true)
	case "Date":
		text, e2 := n.value("Date", true)
		if e2 != nil {
			return nil, e2
		}
		date, e2 := time.Parse(time.RFC3339, text)
		if e2 != nil {
			return nil, ErrInvalid
		}
		e.Date = &date
	case "ExpiredObjectDeleteMarker":
		text, e2 := n.value("ExpiredObjectDeleteMarker", true)
		if e2 != nil {
			return nil, e2
		}
		value := false
		switch strings.TrimSpace(text) {
		case "true", "1":
			value = true
		case "false", "0":
		default:
			return nil, ErrInvalid
		}
		e.ExpiredObjectDeleteMarker = &value
	}
	return e, err
}
