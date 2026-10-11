package api

// ADR-962: edge-rule match expressions. One structured condition language
// for every rule kind, validated, compiled and evaluated here so the gateway
// and the trace simulator share a single implementation.

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Match-expression bounds (ADR-962 §3).
const (
	EdgeRuleMatchMaxDepth       = 4
	EdgeRuleMatchMaxNodes       = 32
	EdgeRuleMatchMaxValues      = 64
	EdgeRuleMatchMaxValueBytes  = 256
	EdgeRuleMatchMaxRegexBytes  = 256
	edgeRuleMatchMaxSelectorLen = 128
)

// EdgeRuleMatchExpr is one node of a match condition: exactly one of All,
// Any, Not, or a leaf (Field + Op + Value/Values).
type EdgeRuleMatchExpr struct {
	All    []EdgeRuleMatchExpr `json:"all,omitempty"`
	Any    []EdgeRuleMatchExpr `json:"any,omitempty"`
	Not    *EdgeRuleMatchExpr  `json:"not,omitempty"`
	Field  string              `json:"field,omitempty"`
	Op     string              `json:"op,omitempty"`
	Value  string              `json:"value,omitempty"`
	Values []string            `json:"values,omitempty"`
	// List names an account list for the in_list op (ADR-963).
	List string `json:"list,omitempty"`
}

// EdgeRuleMatchInput is the request snapshot a condition is evaluated
// against. ClientIP / Country are empty when the gateway has no trusted
// value; such a field is absent (ADR-962 §5).
type EdgeRuleMatchInput struct {
	Method   string
	Path     string
	Host     string
	Headers  http.Header
	Query    url.Values
	ClientIP net.IP
	Country  string
	// ASN is the client IP's autonomous system (ADR-966); 0 is absent.
	ASN uint32
}

type matchFieldKind int

const (
	fieldMethod matchFieldKind = iota
	fieldPath
	fieldHost
	fieldClientIP
	fieldCountry
	fieldHeader
	fieldCookie
	fieldQuery
	fieldASN
)

// EdgeRuleMatchProgram is a validated, compiled condition. It is immutable
// and safe for concurrent evaluation. A nil program always matches.
type EdgeRuleMatchProgram struct {
	root matchNode
}

type matchNode struct {
	all, any []matchNode
	not      *matchNode
	field    matchFieldKind
	name     string // header / cookie / query name
	op       string
	values   []string
	foldCase bool
	re       *regexp.Regexp
	nets     []*net.IPNet
	list     *EdgeRuleList
}

// matchCompiler carries the node budget and the account's lists through
// one compilation.
type matchCompiler struct {
	nodes int
	lists EdgeRuleLists
	// shapeOnly accepts unresolved list references (ValidateEdgeRuleMatch);
	// such a program is never evaluated.
	shapeOnly bool
}

// CompileEdgeRuleMatch validates expr and compiles it. A nil expr compiles
// to a nil program, which always matches. An in_list leaf fails to compile;
// use CompileEdgeRuleMatchWithLists for conditions that reference lists.
func CompileEdgeRuleMatch(expr *EdgeRuleMatchExpr) (*EdgeRuleMatchProgram, error) {
	return CompileEdgeRuleMatchWithLists(expr, nil)
}

// CompileEdgeRuleMatchWithLists compiles expr, resolving in_list references
// against lists (ADR-963). An unknown list, or one whose kind does not fit
// the field, is a compile error.
func CompileEdgeRuleMatchWithLists(expr *EdgeRuleMatchExpr, lists EdgeRuleLists) (*EdgeRuleMatchProgram, error) {
	if expr == nil {
		return nil, nil
	}
	c := &matchCompiler{lists: lists}
	root, err := c.node(*expr, 1, "match")
	if err != nil {
		return nil, err
	}
	return &EdgeRuleMatchProgram{root: root}, nil
}

// NeverMatchingEdgeRuleProgram is a condition that holds for no request. The
// gateway uses it for a stored condition that fails to compile.
func NeverMatchingEdgeRuleProgram() *EdgeRuleMatchProgram {
	return &EdgeRuleMatchProgram{root: matchNode{any: []matchNode{}}}
}

// ValidateEdgeRuleMatch reports a validation Problem for an invalid
// condition (nil is valid). It checks shape only: in_list references are
// accepted unresolved; apid resolves them with ValidateEdgeRuleMatchWithLists.
func ValidateEdgeRuleMatch(expr *EdgeRuleMatchExpr) *Problem {
	if expr == nil {
		return nil
	}
	c := &matchCompiler{shapeOnly: true}
	if _, err := c.node(*expr, 1, "match"); err != nil {
		return ErrValidation(err.Error())
	}
	return nil
}

// ValidateEdgeRuleMatchWithLists is ValidateEdgeRuleMatch with in_list
// references resolved against lists.
func ValidateEdgeRuleMatchWithLists(expr *EdgeRuleMatchExpr, lists EdgeRuleLists) *Problem {
	if _, err := CompileEdgeRuleMatchWithLists(expr, lists); err != nil {
		return ErrValidation(err.Error())
	}
	return nil
}

func (c *matchCompiler) node(e EdgeRuleMatchExpr, depth int, at string) (matchNode, error) {
	c.nodes++
	if c.nodes > EdgeRuleMatchMaxNodes {
		return matchNode{}, fmt.Errorf("match: more than %d nodes", EdgeRuleMatchMaxNodes)
	}
	if depth > EdgeRuleMatchMaxDepth {
		return matchNode{}, fmt.Errorf("%s: nested deeper than %d levels", at, EdgeRuleMatchMaxDepth)
	}
	isLeaf := e.Field != "" || e.Op != "" || e.Value != "" || len(e.Values) > 0 || e.List != ""
	shapes := 0
	for _, set := range []bool{len(e.All) > 0, len(e.Any) > 0, e.Not != nil, isLeaf} {
		if set {
			shapes++
		}
	}
	if shapes != 1 {
		return matchNode{}, fmt.Errorf("%s: a node must be exactly one of all, any, not, or a field/op leaf", at)
	}
	switch {
	case len(e.All) > 0 || len(e.Any) > 0:
		children, key := e.All, "all"
		if len(e.Any) > 0 {
			children, key = e.Any, "any"
		}
		out := make([]matchNode, 0, len(children))
		for i, child := range children {
			n, err := c.node(child, depth+1, fmt.Sprintf("%s.%s[%d]", at, key, i))
			if err != nil {
				return matchNode{}, err
			}
			out = append(out, n)
		}
		if key == "all" {
			return matchNode{all: out}, nil
		}
		return matchNode{any: out}, nil
	case e.Not != nil:
		n, err := c.node(*e.Not, depth+1, at+".not")
		if err != nil {
			return matchNode{}, err
		}
		return matchNode{not: &n}, nil
	}
	return c.leaf(e, at)
}

func (c *matchCompiler) leaf(e EdgeRuleMatchExpr, at string) (matchNode, error) {
	kind, name, err := parseMatchField(e.Field)
	if err != nil {
		return matchNode{}, fmt.Errorf("%s: %w", at, err)
	}
	n := matchNode{op: e.Op, field: kind, name: name}
	n.foldCase = kind == fieldMethod || kind == fieldHost || kind == fieldCountry
	if e.Op == "in_list" || e.List != "" {
		return c.listLeaf(n, e, at)
	}
	values := e.Values
	if e.Value != "" {
		if len(e.Values) > 0 {
			return matchNode{}, fmt.Errorf("%s: use value or values, not both", at)
		}
		values = []string{e.Value}
	}
	if len(values) > EdgeRuleMatchMaxValues {
		return matchNode{}, fmt.Errorf("%s: more than %d values", at, EdgeRuleMatchMaxValues)
	}
	for _, v := range values {
		if len(v) > EdgeRuleMatchMaxValueBytes {
			return matchNode{}, fmt.Errorf("%s: value longer than %d bytes", at, EdgeRuleMatchMaxValueBytes)
		}
	}
	switch e.Op {
	case "exists", "missing":
		if len(values) > 0 {
			return matchNode{}, fmt.Errorf("%s: op %q takes no value", at, e.Op)
		}
	case "eq", "ne":
		if len(values) != 1 {
			return matchNode{}, fmt.Errorf("%s: op %q takes one value (use in / not_in for several)", at, e.Op)
		}
	case "in", "not_in", "prefix", "suffix", "contains":
		if len(values) == 0 {
			return matchNode{}, fmt.Errorf("%s: op %q needs at least one value", at, e.Op)
		}
	case "regex":
		if len(values) != 1 || len(values[0]) > EdgeRuleMatchMaxRegexBytes {
			return matchNode{}, fmt.Errorf("%s: regex takes one pattern of at most %d bytes", at, EdgeRuleMatchMaxRegexBytes)
		}
		re, err := regexp.Compile(values[0])
		if err != nil {
			return matchNode{}, fmt.Errorf("%s: invalid regex: %w", at, err)
		}
		n.re = re
	case "cidr":
		if kind != fieldClientIP {
			return matchNode{}, fmt.Errorf("%s: op cidr applies only to client_ip", at)
		}
		if len(values) == 0 {
			return matchNode{}, fmt.Errorf("%s: op cidr needs at least one CIDR", at)
		}
		for _, v := range values {
			_, ipnet, err := net.ParseCIDR(v)
			if err != nil {
				return matchNode{}, fmt.Errorf("%s: invalid CIDR %q", at, v)
			}
			n.nets = append(n.nets, ipnet)
		}
	default:
		return matchNode{}, fmt.Errorf("%s: unknown op %q", at, e.Op)
	}
	if kind == fieldASN {
		switch e.Op {
		case "exists", "missing":
		case "eq", "ne", "in", "not_in":
			canonical := make([]string, len(values))
			for i, v := range values {
				asn, err := canonicalASN(v)
				if err != nil {
					return matchNode{}, fmt.Errorf("%s: %w", at, err)
				}
				canonical[i] = asn
			}
			values = canonical
		default:
			return matchNode{}, fmt.Errorf("%s: asn supports eq, ne, in, not_in, exists, missing, in_list", at)
		}
	}
	if kind == fieldClientIP {
		switch e.Op {
		case "cidr", "exists", "missing":
		case "eq", "ne", "in", "not_in":
			// Canonicalize so "2001:DB8::1" and "2001:db8:0::1" compare equal
			// to the gateway's net.IP rendering.
			canonical := make([]string, len(values))
			for i, v := range values {
				ip := net.ParseIP(v)
				if ip == nil {
					return matchNode{}, fmt.Errorf("%s: invalid IP %q", at, v)
				}
				canonical[i] = ip.String()
			}
			values = canonical
		default:
			return matchNode{}, fmt.Errorf("%s: client_ip supports cidr, eq, ne, in, not_in, exists, missing", at)
		}
	}
	n.values = values
	if n.foldCase {
		folded := make([]string, len(values))
		for i, v := range values {
			folded[i] = strings.ToLower(v)
		}
		n.values = folded
	}
	return n, nil
}

// listLeaf compiles an ADR-963 in_list leaf.
func (c *matchCompiler) listLeaf(n matchNode, e EdgeRuleMatchExpr, at string) (matchNode, error) {
	if e.Op != "in_list" {
		return matchNode{}, fmt.Errorf("%s: list applies only to op in_list", at)
	}
	if e.List == "" || e.Value != "" || len(e.Values) > 0 {
		return matchNode{}, fmt.Errorf("%s: op in_list takes a list name and no value", at)
	}
	if err := ValidateEdgeRuleListName(e.List); err != nil {
		return matchNode{}, fmt.Errorf("%s: %w", at, err)
	}
	list, ok := c.lists[e.List]
	if !ok || list == nil {
		if c.shapeOnly {
			return n, nil
		}
		return matchNode{}, fmt.Errorf("%s: unknown list %q", at, e.List)
	}
	if !edgeRuleListFits(list.Kind, n.field) {
		return matchNode{}, fmt.Errorf("%s: a %s list cannot match field %q (ip: client_ip, country: country, host: host, string: path, header:, cookie:, query:)", at, list.Kind, e.Field)
	}
	n.list = list
	return n, nil
}

func parseMatchField(field string) (matchFieldKind, string, error) {
	switch field {
	case "method":
		return fieldMethod, "", nil
	case "path":
		return fieldPath, "", nil
	case "host":
		return fieldHost, "", nil
	case "client_ip":
		return fieldClientIP, "", nil
	case "country":
		return fieldCountry, "", nil
	case "asn":
		return fieldASN, "", nil
	}
	prefix, name, ok := strings.Cut(field, ":")
	if ok && name != "" && len(name) <= edgeRuleMatchMaxSelectorLen {
		switch prefix {
		case "header":
			return fieldHeader, http.CanonicalHeaderKey(name), nil
		case "cookie":
			return fieldCookie, name, nil
		case "query":
			return fieldQuery, name, nil
		}
	}
	return 0, "", fmt.Errorf("unknown field %q (method, path, host, client_ip, country, asn, header:<name>, cookie:<name>, query:<name>)", field)
}

// Matches evaluates the condition. A nil program always matches.
func (p *EdgeRuleMatchProgram) Matches(in EdgeRuleMatchInput) bool {
	if p == nil {
		return true
	}
	return p.root.eval(in)
}

func (n *matchNode) eval(in EdgeRuleMatchInput) bool {
	switch {
	case n.all != nil:
		for i := range n.all {
			if !n.all[i].eval(in) {
				return false
			}
		}
		return true
	case n.any != nil:
		for i := range n.any {
			if n.any[i].eval(in) {
				return true
			}
		}
		return false
	case n.not != nil:
		return !n.not.eval(in)
	}
	return n.evalLeaf(in)
}

// evalLeaf applies the op to the field's observed values. An absent field
// satisfies only "missing". A repeated header or query parameter matches
// when any value does, except ne / not_in, which require every value to
// differ, so "header X ne v" means "no X header equals v".
func (n *matchNode) evalLeaf(in EdgeRuleMatchInput) bool {
	values, present := n.fieldValues(in)
	switch n.op {
	case "exists":
		return present
	case "missing":
		return !present
	}
	if !present {
		return false
	}
	if n.op == "in_list" && n.list == nil {
		return false
	}
	if n.op == "in_list" && n.field == fieldClientIP {
		return n.list.containsIP(in.ClientIP)
	}
	if n.op == "cidr" {
		for _, ipnet := range n.nets {
			if ipnet.Contains(in.ClientIP) {
				return true
			}
		}
		return false
	}
	every := n.op == "ne" || n.op == "not_in"
	for _, got := range values {
		if n.foldCase {
			got = strings.ToLower(got)
		}
		ok := n.compare(got)
		if every && !ok {
			return false
		}
		if !every && ok {
			return true
		}
	}
	return every
}

func (n *matchNode) compare(got string) bool {
	switch n.op {
	case "eq":
		return got == n.values[0]
	case "ne":
		return got != n.values[0]
	case "in":
		return containsMatchValue(n.values, got)
	case "not_in":
		return !containsMatchValue(n.values, got)
	case "prefix":
		return anyMatchValue(n.values, func(v string) bool { return strings.HasPrefix(got, v) })
	case "suffix":
		return anyMatchValue(n.values, func(v string) bool { return strings.HasSuffix(got, v) })
	case "contains":
		return anyMatchValue(n.values, func(v string) bool { return strings.Contains(got, v) })
	case "regex":
		return n.re.MatchString(got)
	case "in_list":
		return n.list.contains(got)
	}
	return false
}

func (n *matchNode) fieldValues(in EdgeRuleMatchInput) ([]string, bool) {
	switch n.field {
	case fieldMethod:
		return []string{in.Method}, in.Method != ""
	case fieldPath:
		return []string{in.Path}, in.Path != ""
	case fieldHost:
		return []string{in.Host}, in.Host != ""
	case fieldCountry:
		return []string{in.Country}, in.Country != ""
	case fieldASN:
		if in.ASN == 0 {
			return nil, false
		}
		return []string{strconv.FormatUint(uint64(in.ASN), 10)}, true
	case fieldClientIP:
		if in.ClientIP == nil {
			return nil, false
		}
		return []string{in.ClientIP.String()}, true
	case fieldHeader:
		vs := in.Headers.Values(n.name)
		return vs, len(vs) > 0
	case fieldCookie:
		r := http.Request{Header: in.Headers}
		c, err := r.Cookie(n.name)
		if err != nil {
			return nil, false
		}
		return []string{c.Value}, true
	case fieldQuery:
		vs := in.Query[n.name]
		return vs, len(vs) > 0
	}
	return nil, false
}

func containsMatchValue(values []string, s string) bool {
	for _, v := range values {
		if v == s {
			return true
		}
	}
	return false
}

func anyMatchValue(values []string, f func(string) bool) bool {
	for _, v := range values {
		if f(v) {
			return true
		}
	}
	return false
}
