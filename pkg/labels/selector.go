package labels

import (
	"fmt"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Operator is how a requirement compares a label.
type Operator string

// Selector operators, as Kubernetes spells them.
const (
	OpEquals       Operator = "="
	OpNotEquals    Operator = "!="
	OpExists       Operator = "exists"
	OpDoesNotExist Operator = "!"
	OpIn           Operator = "in"
	OpNotIn        Operator = "notin"
)

// Requirement is one comma-separated term of a selector. Values holds one
// value for = and !=, the set for in and notin, and nothing otherwise.
type Requirement struct {
	Key      string
	Operator Operator
	Values   []string
}

// Matches reports whether labels satisfy the requirement. Like Kubernetes,
// != and notin match a resource that does not carry the key.
func (r Requirement) Matches(labels map[string]string) bool {
	value, ok := labels[r.Key]
	switch r.Operator {
	case OpEquals:
		return ok && value == r.value()
	case OpNotEquals:
		return !ok || value != r.value()
	case OpExists:
		return ok
	case OpDoesNotExist:
		return !ok
	case OpIn:
		return ok && slices.Contains(r.Values, value)
	case OpNotIn:
		return !ok || !slices.Contains(r.Values, value)
	}
	return false
}

func (r Requirement) String() string {
	switch r.Operator {
	case OpExists:
		return r.Key
	case OpDoesNotExist:
		return "!" + r.Key
	case OpIn, OpNotIn:
		return fmt.Sprintf("%s %s (%s)", r.Key, r.Operator, strings.Join(r.Values, ","))
	}
	return r.Key + string(r.Operator) + r.value()
}

// value is the single value of = and !=; a requirement built without one
// compares against the empty value.
func (r Requirement) value() string {
	if len(r.Values) == 0 {
		return ""
	}
	return r.Values[0]
}

// Selector is a parsed label selector: every requirement must hold. The zero
// Selector has no requirements and matches every resource.
type Selector struct {
	Requirements []Requirement
}

// Empty reports whether the selector has no requirements.
func (s Selector) Empty() bool { return len(s.Requirements) == 0 }

// Matches reports whether labels satisfy every requirement.
func (s Selector) Matches(labels map[string]string) bool {
	for _, r := range s.Requirements {
		if !r.Matches(labels) {
			return false
		}
	}
	return true
}

// String renders the selector in canonical form: requirements in the order
// given, set values sorted and deduplicated.
func (s Selector) String() string {
	parts := make([]string, len(s.Requirements))
	for i, r := range s.Requirements {
		parts[i] = r.String()
	}
	return strings.Join(parts, ",")
}

// Parse reads a Kubernetes label selector such as
// "team=billing,tier in (web,api),!canary". An empty or blank selector
// matches everything. Keys and values must be valid labels; reserved keys
// may be selected.
func Parse(selector string) (Selector, error) {
	if len(selector) > api.LabelSelectorMaxBytes {
		return Selector{}, &Error{
			Code:     CodeInvalidSelector,
			Message:  fmt.Sprintf("the selector is %d bytes; the limit is %d", len(selector), api.LabelSelectorMaxBytes),
			Limit:    api.LabelSelectorMaxBytes,
			Observed: len(selector),
		}
	}
	p := parser{src: selector}
	p.next()
	if p.tok.kind == tokEOF {
		return Selector{}, nil
	}
	var out Selector
	for {
		r, err := p.requirement()
		if err != nil {
			return Selector{}, err
		}
		out.Requirements = append(out.Requirements, r)
		switch p.tok.kind {
		case tokEOF:
			return out, nil
		case tokComma:
			p.next()
		default:
			return Selector{}, p.fail("expected ',' or the end of the selector")
		}
	}
}

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokIdent
	tokComma
	tokOpen
	tokClose
	tokEquals    // = or ==
	tokNotEquals // !=
	tokNot       // !
)

type token struct {
	kind tokenKind
	text string
	pos  int
}

type parser struct {
	src   string
	pos   int
	tok   token
	start int // where the current requirement starts, for error messages
}

// next reads the following token. An identifier is a run of characters that
// are not whitespace or one of ",()=!"; whether it is a valid key, value or
// keyword is decided by the grammar.
func (p *parser) next() {
	for p.pos < len(p.src) && isSpace(p.src[p.pos]) {
		p.pos++
	}
	start := p.pos
	if p.pos == len(p.src) {
		p.tok = token{kind: tokEOF, pos: start}
		return
	}
	switch p.src[p.pos] {
	case ',':
		p.pos++
		p.tok = token{kind: tokComma, text: ",", pos: start}
	case '(':
		p.pos++
		p.tok = token{kind: tokOpen, text: "(", pos: start}
	case ')':
		p.pos++
		p.tok = token{kind: tokClose, text: ")", pos: start}
	case '=':
		p.pos++
		if p.pos < len(p.src) && p.src[p.pos] == '=' {
			p.pos++
		}
		p.tok = token{kind: tokEquals, text: p.src[start:p.pos], pos: start}
	case '!':
		p.pos++
		if p.pos < len(p.src) && p.src[p.pos] == '=' {
			p.pos++
			p.tok = token{kind: tokNotEquals, text: "!=", pos: start}
			return
		}
		p.tok = token{kind: tokNot, text: "!", pos: start}
	default:
		for p.pos < len(p.src) && !isSpace(p.src[p.pos]) && !strings.ContainsRune(",()=!", rune(p.src[p.pos])) {
			p.pos++
		}
		p.tok = token{kind: tokIdent, text: p.src[start:p.pos], pos: start}
	}
}

func (p *parser) requirement() (Requirement, error) {
	p.start = p.tok.pos
	if p.tok.kind == tokNot {
		p.next()
		key, err := p.key()
		if err != nil {
			return Requirement{}, err
		}
		return Requirement{Key: key, Operator: OpDoesNotExist}, nil
	}
	key, err := p.key()
	if err != nil {
		return Requirement{}, err
	}
	switch p.tok.kind {
	case tokEOF, tokComma:
		return Requirement{Key: key, Operator: OpExists}, nil
	case tokEquals, tokNotEquals:
		op := OpEquals
		if p.tok.kind == tokNotEquals {
			op = OpNotEquals
		}
		p.next()
		value, err := p.value(key)
		if err != nil {
			return Requirement{}, err
		}
		return Requirement{Key: key, Operator: op, Values: []string{value}}, nil
	case tokIdent:
		var op Operator
		switch p.tok.text {
		case string(OpIn):
			op = OpIn
		case string(OpNotIn):
			op = OpNotIn
		default:
			return Requirement{}, p.fail(fmt.Sprintf("unknown operator %q; use =, ==, !=, in or notin", p.tok.text))
		}
		p.next()
		values, err := p.set(key)
		if err != nil {
			return Requirement{}, err
		}
		return Requirement{Key: key, Operator: op, Values: values}, nil
	}
	return Requirement{}, p.fail("expected an operator after the key")
}

func (p *parser) key() (string, error) {
	if p.tok.kind != tokIdent {
		return "", p.fail("expected a label key")
	}
	key := p.tok.text
	if err := ValidateKey(key); err != nil {
		return "", p.fail(err.Error())
	}
	p.next()
	return key, nil
}

// value reads an optional value: "key=" selects the empty value.
func (p *parser) value(key string) (string, error) {
	if p.tok.kind != tokIdent {
		return "", nil
	}
	value := p.tok.text
	if err := ValidateValue(key, value); err != nil {
		return "", p.fail(err.Error())
	}
	p.next()
	return value, nil
}

// set reads "(a,b,...)". A value may be empty, so "()" is the set holding
// only the empty value, as in Kubernetes.
func (p *parser) set(key string) ([]string, error) {
	if p.tok.kind != tokOpen {
		return nil, p.fail("expected '(' and a list of values")
	}
	p.next()
	var values []string
	for {
		value, err := p.value(key)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
		switch p.tok.kind {
		case tokComma:
			p.next()
		case tokClose:
			p.next()
			slices.Sort(values)
			return slices.Compact(values), nil
		default:
			return nil, p.fail("expected ',' or ')' in the value list")
		}
	}
}

// fail names the requirement being parsed: its text from where it starts to
// the next top-level comma or the end of the selector.
func (p *parser) fail(problem string) error {
	end, depth := len(p.src), 0
	for i := p.start; i < len(p.src); i++ {
		switch p.src[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth <= 0 {
				end = i
			}
		}
		if end != len(p.src) {
			break
		}
	}
	return &Error{Code: CodeInvalidSelector, Key: strings.TrimSpace(p.src[p.start:end]), Message: problem}
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
