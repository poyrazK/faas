package objectstorage

import (
	"encoding/xml"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
)

func ParseObjectNotificationsXML(body []byte) ([]api.ObjectNotificationRule, error) {
	n, err := readBoundedObjectXML(body, api.MaxObjectNotificationBodyBytes, api.MaxObjectNotificationXMLDepth, api.MaxObjectNotificationXMLNodes)
	if err != nil || n.name != "NotificationConfiguration" {
		return nil, ErrInvalid
	}
	for _, c := range n.children {
		if c.name == "TopicConfiguration" || c.name == "EventBridgeConfiguration" {
			return nil, ErrUnsupported
		}
	}
	if err = n.fields("QueueConfiguration", "CloudFunctionConfiguration"); err != nil {
		return nil, err
	}
	rules := []api.ObjectNotificationRule{}
	for _, c := range n.children {
		r, e := parseObjectNotificationXMLRule(c)
		if e != nil {
			return nil, e
		}
		rules = append(rules, r)
	}
	normal, err := api.NormalizeObjectNotificationRules(rules)
	if err != nil {
		return nil, ErrInvalid
	}
	return normal, nil
}
func parseObjectNotificationXMLRule(n *lifecycleXMLNode) (api.ObjectNotificationRule, error) {
	r := api.ObjectNotificationRule{}
	target := "Queue"
	kind := "queue"
	if n.name == "CloudFunctionConfiguration" {
		target, kind = "CloudFunction", "function"
	}
	if err := n.fields("Id", target, "Event", "Filter"); err != nil {
		return r, err
	}
	var err error
	if r.ID, err = n.value("Id", false); err != nil {
		return r, err
	}
	if r.Destination, err = n.value(target, true); err != nil {
		return r, err
	}
	t, err := api.ParseObjectNotificationTarget(r.Destination)
	if err != nil || t.Kind != kind {
		return r, ErrInvalid
	}
	for _, c := range n.children {
		if c.name == "Event" {
			v, e := c.scalar()
			if e != nil {
				return r, e
			}
			r.Events = append(r.Events, v)
		}
	}
	f, err := n.one("Filter", false)
	if err != nil || f == nil {
		return r, err
	}
	if err = f.fields("S3Key"); err != nil {
		return r, err
	}
	key, err := f.one("S3Key", true)
	if err != nil {
		return r, err
	}
	if err = key.fields("FilterRule"); err != nil {
		return r, err
	}
	seen := map[string]bool{}
	for _, c := range key.children {
		if err = c.fields("Name", "Value"); err != nil {
			return r, err
		}
		name, e := c.value("Name", true)
		if e != nil || seen[name] {
			return r, ErrInvalid
		}
		seen[name] = true
		value, e := c.value("Value", true)
		if e != nil {
			return r, e
		}
		value, e = url.QueryUnescape(value)
		if e != nil {
			return r, ErrInvalid
		}
		switch name {
		case "prefix":
			r.Prefix = value
		case "suffix":
			r.Suffix = value
		default:
			return r, ErrInvalid
		}
	}
	return r, nil
}

type notificationXML struct {
	XMLName   xml.Name              `xml:"NotificationConfiguration"`
	XMLNS     string                `xml:"xmlns,attr"`
	Queues    []notificationXMLRule `xml:"QueueConfiguration"`
	Functions []notificationXMLRule `xml:"CloudFunctionConfiguration"`
}
type notificationXMLRule struct {
	ID       string                 `xml:"Id"`
	Queue    string                 `xml:"Queue,omitempty"`
	Function string                 `xml:"CloudFunction,omitempty"`
	Events   []string               `xml:"Event"`
	Filter   *notificationXMLFilter `xml:"Filter,omitempty"`
}
type notificationXMLFilter struct {
	Rules []notificationXMLFilterRule `xml:"S3Key>FilterRule"`
}
type notificationXMLFilterRule struct {
	Name  string `xml:"Name"`
	Value string `xml:"Value"`
}

func MarshalObjectNotificationsXML(in []api.ObjectNotificationRule) ([]byte, error) {
	rules, err := api.NormalizeObjectNotificationRules(in)
	if err != nil {
		return nil, ErrInvalid
	}
	out := notificationXML{XMLNS: taggingXMLNamespace}
	for _, r := range rules {
		x := notificationXMLRule{ID: r.ID, Events: r.Events}
		t, e := api.ParseObjectNotificationTarget(r.Destination)
		if e != nil {
			return nil, e
		}
		f := notificationXMLFilter{}
		if r.Prefix != "" {
			f.Rules = append(f.Rules, notificationXMLFilterRule{"prefix", url.QueryEscape(r.Prefix)})
		}
		if r.Suffix != "" {
			f.Rules = append(f.Rules, notificationXMLFilterRule{"suffix", url.QueryEscape(r.Suffix)})
		}
		if len(f.Rules) > 0 {
			x.Filter = &f
		}
		if t.Kind == "queue" {
			x.Queue = r.Destination
			out.Queues = append(out.Queues, x)
		} else {
			x.Function = r.Destination
			out.Functions = append(out.Functions, x)
		}
	}
	return xml.Marshal(out)
}
