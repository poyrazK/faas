// Package connectchecks prepares reviewed read-only hosted access assertions.
package connectchecks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
)

const MaxInputBytes = 32768

var (
	identifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
	columnName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)
	textKey    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	integerKey = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)
)

type Fixture struct {
	Name      string          `json:"name"`
	Key       json.RawMessage `json:"key"`
	VisibleTo []string        `json:"visible_to"`
}

type Spec struct {
	Version    int       `json:"version"`
	Status     string    `json:"status"`
	Relation   string    `json:"relation"`
	KeyColumn  string    `json:"key_column"`
	Identities []string  `json:"identities"`
	Fixtures   []Fixture `json:"fixtures"`
}

type Expectation struct {
	Status int `json:"status"`
	JSON   any `json:"json"`
}
type Request struct {
	Name     string      `json:"name"`
	Method   string      `json:"method"`
	Path     string      `json:"path"`
	Identity string      `json:"identity"`
	Expect   Expectation `json:"expect"`
}
type Collection struct {
	Version  int       `json:"version"`
	Requests []Request `json:"requests"`
}
type Summary struct {
	Fixtures int `json:"fixtures"`
	Allowed  int `json:"allowed"`
	Denied   int `json:"denied"`
}

// Decode rejects unknown and duplicate JSON keys and oversized input, so the
// reviewed document has a single unambiguous interpretation.
func Decode(data []byte) (Spec, error) {
	var spec Spec
	if len(data) == 0 || len(data) > MaxInputBytes {
		return spec, errors.New("checks specification must be at most 32 KiB")
	}
	check := json.NewDecoder(bytes.NewReader(data))
	check.UseNumber()
	if err := checkValue(check, 0); err != nil {
		return spec, errors.New("checks specification is invalid or has duplicate keys")
	}
	if _, err := check.Token(); err != io.EOF {
		return spec, errors.New("checks specification must contain one JSON document")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&spec); err != nil {
		return Spec{}, errors.New("invalid checks specification")
	}
	return spec, nil
}

func checkValue(dec *json.Decoder, depth int) error {
	if depth > 16 {
		return errors.New("too deeply nested")
	}
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return errors.New("duplicate or invalid key")
			}
			seen[name] = true
			if err := checkValue(dec, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := checkValue(dec, depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("unexpected delimiter")
	}
	_, err = dec.Token()
	return err
}

// Build expands each reviewed fixture across both identities. Each fixture
// and each identity must have a positive expectation, so missing rows or
// identities cannot make an all-denied matrix appear successful.
func Build(spec Spec) (Collection, Summary, error) {
	var summary Summary
	fail := func(message string) (Collection, Summary, error) { return Collection{}, Summary{}, errors.New(message) }
	if spec.Version != 1 || spec.Status != "reviewed" {
		return fail("set version to 1 and status to reviewed after reviewing the access matrix")
	}
	if !columnName.MatchString(spec.Relation) || spec.Relation == "rpc" || !columnName.MatchString(spec.KeyColumn) {
		return fail("use supported public relation and key column identifiers")
	}
	for _, reserved := range []string{"select", "limit", "offset", "order", "and", "or", "not"} {
		if spec.KeyColumn == reserved {
			return fail("key column conflicts with a query parameter")
		}
	}
	if len(spec.Identities) != 2 || spec.Identities[0] == spec.Identities[1] {
		return fail("choose two distinct named identities")
	}
	for _, name := range spec.Identities {
		if !validName(name) {
			return fail("use valid named identities")
		}
	}
	if len(spec.Fixtures) < 2 || len(spec.Fixtures) > 20 {
		return fail("choose 2 to 20 distinct fixtures")
	}
	collection := Collection{Version: 1, Requests: make([]Request, 0, 2*len(spec.Fixtures))}
	seenNames, seenKeys, positive := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for index, fixture := range spec.Fixtures {
		if !validName(fixture.Name) || seenNames[fixture.Name] {
			return fail("fixture names must be unique identifiers")
		}
		seenNames[fixture.Name] = true
		key, rendered, err := parseKey(fixture.Key)
		if err != nil {
			return fail("fixture keys must be simple strings or exact safe integers")
		}
		if seenKeys[rendered] {
			return fail("fixture keys must be distinct")
		}
		seenKeys[rendered] = true
		visible := map[string]bool{}
		for _, name := range fixture.VisibleTo {
			if (name != spec.Identities[0] && name != spec.Identities[1]) || visible[name] {
				return fail("visible_to must contain unique chosen identities")
			}
			visible[name] = true
			positive[name] = true
		}
		if len(visible) == 0 {
			return fail("each fixture needs a positive visibility expectation")
		}
		query := url.Values{"select": {spec.KeyColumn}, "limit": {"2"}, spec.KeyColumn: {"eq." + rendered}}
		label := fixture.Name
		if len(label) > 24 {
			label = label[:24]
		}
		for _, name := range spec.Identities {
			rows := []any{}
			if visible[name] {
				rows = append(rows, map[string]any{spec.KeyColumn: key})
				summary.Allowed++
			} else {
				summary.Denied++
			}
			collection.Requests = append(collection.Requests, Request{Name: fmt.Sprintf("%s %d as %s", label, index+1, name), Method: "GET", Path: "/rest/v1/" + spec.Relation + "?" + query.Encode(), Identity: name, Expect: Expectation{Status: 200, JSON: rows}})
		}
	}
	for _, name := range spec.Identities {
		if !positive[name] {
			return fail("each identity needs a positive visibility expectation")
		}
	}
	summary.Fixtures = len(spec.Fixtures)
	return collection, summary, nil
}

func validName(name string) bool {
	return identifier.MatchString(name) && name != "constructor" && name != "prototype" && name != "__proto__"
}

func parseKey(raw json.RawMessage) (any, string, error) {
	var value any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&value); err != nil {
		return nil, "", err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, "", errors.New("invalid fixture key")
	}
	switch v := value.(type) {
	case string:
		if textKey.MatchString(v) {
			return v, v, nil
		}
	case json.Number:
		if integerKey.MatchString(string(v)) {
			n, err := strconv.ParseInt(string(v), 10, 64)
			if err == nil && n >= -9007199254740991 && n <= 9007199254740991 {
				return v, strconv.FormatInt(n, 10), nil
			}
		}
	}
	return nil, "", errors.New("unsupported fixture key")
}
