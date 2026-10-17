// Package rehearsalobj defines bounded independent object rehearsal evidence (ADR-20261017201010).
package rehearsalobj

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	Version             = 1
	ProxyPort           = 10084
	MaxObjects          = 32
	MaxObjectBytes      = 64 << 10
	MaxContentBytes     = 256 << 10
	MaxRequests         = 256
	MaxEvidenceBytes    = 1 << 20
	Provenance          = "platform_object_collector"
	CredentialNamespace = "rehearsal_object_credential"
	Region              = "us-east-1"
)

var digest = regexp.MustCompile(`^[a-f0-9]{64}$`)
var bucketName = regexp.MustCompile(`^[a-z][a-z0-9-]{1,29}[a-z0-9]$`)
var metadataName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

type Spec struct {
	Version       int    `json:"version" yaml:"version"`
	Bucket        string `json:"bucket" yaml:"bucket"`
	FixtureSHA256 string `json:"fixture_sha256" yaml:"-"`
}

// Data is accepted only as fixture input and private platform state, never evidence.
type Data struct {
	Key         string            `json:"key"`
	Body        []byte            `json:"body"`
	ContentType string            `json:"content_type"`
	Metadata    map[string]string `json:"metadata"`
}

type Object struct {
	Key         string            `json:"key"`
	Size        int64             `json:"size"`
	SHA256      string            `json:"sha256"`
	ContentType string            `json:"content_type"`
	Metadata    map[string]string `json:"metadata"`
}

type CreateRequest struct {
	AppSlug string `json:"app_slug"`
	Spec    Spec   `json:"spec"`
	Objects []Data `json:"objects"`
}
type SealRequest struct {
	DeploymentID string `json:"deployment_id"`
}
type Receipt struct {
	Version        int    `json:"version"`
	RunID          string `json:"run_id"`
	AppID          string `json:"app_id"`
	ResourceID     string `json:"resource_id"`
	FixtureSHA256  string `json:"fixture_sha256"`
	ContractSHA256 string `json:"contract_sha256"`
	InitialSHA256  string `json:"initial_sha256"`
	CollectorID    string `json:"collector_id"`
}
type Mutation struct {
	Workload string  `json:"workload,omitempty"`
	Sequence int     `json:"sequence"`
	Method   string  `json:"method"`
	Key      string  `json:"key"`
	Status   int     `json:"status"`
	Object   *Object `json:"object,omitempty"`
}
type Evidence struct {
	Receipt
	Status           string     `json:"status"`
	Provenance       string     `json:"provenance"`
	DeploymentID     string     `json:"deployment_id"`
	Spec             Spec       `json:"spec"`
	Before           []Object   `json:"before"`
	After            []Object   `json:"after"`
	Mutations        []Mutation `json:"mutations"`
	CleanupConfirmed bool       `json:"cleanup_confirmed"`
	CollectorClosed  bool       `json:"collector_closed"`
	Reason           string     `json:"reason,omitempty"`
}
type Comparison struct {
	Status          string           `json:"status"`
	Reason          string           `json:"reason,omitempty"`
	BeforeObjects   []Object         `json:"before_objects,omitempty"`
	AfterObjects    []Object         `json:"after_objects,omitempty"`
	BeforeMutations int              `json:"before_mutations"`
	AfterMutations  int              `json:"after_mutations"`
	MutationChanges []MutationChange `json:"mutation_changes,omitempty"`
}

type MutationChange struct {
	Workload    string  `json:"workload,omitempty"`
	Method      string  `json:"method"`
	Key         string  `json:"key"`
	Object      *Object `json:"object,omitempty"`
	BeforeCount int     `json:"before_count"`
	AfterCount  int     `json:"after_count"`
}

func changedMutations(before, after []Mutation) []MutationChange {
	counts := map[string]MutationChange{}
	for side, list := range [][]Mutation{before, after} {
		for _, m := range list {
			m.Sequence = 0
			key := Hash(m)
			c := counts[key]
			c.Method, c.Key, c.Object, c.Workload = m.Method, m.Key, m.Object, m.Workload
			if side == 0 {
				c.BeforeCount++
			} else {
				c.AfterCount++
			}
			counts[key] = c
		}
	}
	keys := make([]string, 0, len(counts))
	for k, c := range counts {
		if c.BeforeCount != c.AfterCount {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := make([]MutationChange, 0, len(keys))
	for _, k := range keys {
		out = append(out, counts[k])
	}
	return out
}

func Hash(v any) string         { b, _ := json.Marshal(v); return HashBytes(b) }
func HashBytes(b []byte) string { d := sha256.Sum256(b); return hex.EncodeToString(d[:]) }
func (s Spec) Validate() error {
	if s.Version != Version || !bucketName.MatchString(s.Bucket) || !digest.MatchString(s.FixtureSHA256) {
		return errors.New("object fixtures require version 1, a plain bucket name and a fixture digest")
	}
	return nil
}
func (s Spec) ContractSHA256() string { s.FixtureSHA256 = ""; return Hash(s) }
func validObjectText(v string) bool {
	if !utf8.ValidString(v) {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func ValidKey(key string) bool {
	return key != "" && len(key) <= 512 && validObjectText(key) && !strings.ContainsAny(key, "\\")
}
func validMetadata(contentType string, metadata map[string]string) bool {
	if contentType == "" || len(contentType) > 256 || !validObjectText(contentType) || len(metadata) > 8 {
		return false
	}
	for k, v := range metadata {
		if !metadataName.MatchString(k) || len(v) > 256 || !validObjectText(v) {
			return false
		}
	}
	return true
}
func CanonicalObjects(objects []Object) ([]Object, error) {
	if len(objects) > MaxObjects {
		return nil, errors.New("object inventory exceeds bound")
	}
	out := make([]Object, 0, len(objects))
	seen := map[string]bool{}
	total := int64(0)
	for _, o := range objects {
		if !ValidKey(o.Key) || seen[o.Key] || o.Size < 0 || o.Size > MaxObjectBytes || !digest.MatchString(o.SHA256) || !validMetadata(o.ContentType, o.Metadata) {
			return nil, errors.New("invalid object inventory")
		}
		seen[o.Key] = true
		total += o.Size
		if o.Metadata == nil {
			o.Metadata = map[string]string{}
		}
		out = append(out, o)
	}
	if total > MaxContentBytes {
		return nil, errors.New("object content exceeds bound")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}
func Snapshot(objects []Data) ([]Object, error) {
	out := make([]Object, 0, len(objects))
	for _, d := range objects {
		out = append(out, Object{Key: d.Key, Size: int64(len(d.Body)), SHA256: HashBytes(d.Body), ContentType: d.ContentType, Metadata: d.Metadata})
	}
	return CanonicalObjects(out)
}
func (r CreateRequest) Validate() error {
	if r.Spec.Validate() != nil {
		return errors.New("invalid object fixture contract")
	}
	s, e := Snapshot(r.Objects)
	if e != nil {
		return e
	}
	if Hash(s) != r.Spec.FixtureSHA256 {
		return errors.New("object fixture bytes do not match digest")
	}
	return nil
}
func (e Evidence) Validate() error {
	if e.Version != Version || e.Status != "complete" || e.Provenance != Provenance || e.Reason != "" || !e.CleanupConfirmed || !e.CollectorClosed || e.CollectorID == "" || e.RunID == "" || e.AppID == "" || e.ResourceID == "" || e.DeploymentID == "" || e.Spec.Validate() != nil || e.ContractSHA256 != e.Spec.ContractSHA256() || e.FixtureSHA256 != e.Spec.FixtureSHA256 || len(e.Mutations) > MaxRequests {
		return errors.New("incomplete object evidence")
	}
	b, err := CanonicalObjects(e.Before)
	if err != nil {
		return err
	}
	a, err := CanonicalObjects(e.After)
	if err != nil {
		return err
	}
	if Hash(b) != e.InitialSHA256 || e.InitialSHA256 != e.FixtureSHA256 {
		return errors.New("object starting data changed")
	}
	current := map[string]Object{}
	for _, o := range b {
		current[o.Key] = o
	}
	sequence := 0
	for _, m := range e.Mutations {
		if m.Sequence != sequence+1 || m.Sequence > MaxRequests || !ValidKey(m.Key) {
			return errors.New("invalid object mutation inventory")
		}
		sequence = m.Sequence
		switch m.Method {
		case "PUT":
			if m.Status != 200 || m.Object == nil || m.Object.Key != m.Key {
				return errors.New("invalid upload receipt")
			}
			if _, err := CanonicalObjects([]Object{*m.Object}); err != nil {
				return err
			}
			current[m.Key] = *m.Object
		case "DELETE":
			if m.Status != 204 || m.Object != nil {
				return errors.New("invalid delete receipt")
			}
			delete(current, m.Key)
		default:
			return errors.New("unsupported mutation receipt")
		}
		stage := make([]Object, 0, len(current))
		for _, o := range current {
			stage = append(stage, o)
		}
		if _, err := CanonicalObjects(stage); err != nil {
			return err
		}
	}
	final := make([]Object, 0, len(current))
	for _, o := range current {
		final = append(final, o)
	}
	final, _ = CanonicalObjects(final)
	if Hash(final) != Hash(a) {
		return errors.New("object final state does not match mutation receipts")
	}
	raw, _ := json.Marshal(e)
	if len(raw) > MaxEvidenceBytes {
		return errors.New("object evidence exceeds bound")
	}
	return nil
}
func mutationMultiset(v []Mutation) []string {
	out := make([]string, 0, len(v))
	for _, m := range v {
		m.Sequence = 0
		out = append(out, Hash(m))
	}
	sort.Strings(out)
	return out
}
func Compare(before, after *Evidence) *Comparison {
	if before == nil && after == nil {
		return nil
	}
	r := &Comparison{Status: "inconclusive"}
	if before == nil || after == nil || before.Validate() != nil || after.Validate() != nil {
		r.Reason = "independent object evidence is incomplete"
		return r
	}
	if before.ContractSHA256 != after.ContractSHA256 || before.FixtureSHA256 != after.FixtureSHA256 || before.InitialSHA256 != after.InitialSHA256 {
		r.Reason = "object collector contract, fixture or starting data changed"
		return r
	}
	r.Status = "passed"
	r.BeforeObjects, _ = CanonicalObjects(before.After)
	r.AfterObjects, _ = CanonicalObjects(after.After)
	r.BeforeMutations = len(before.Mutations)
	r.AfterMutations = len(after.Mutations)
	r.MutationChanges = changedMutations(before.Mutations, after.Mutations)
	if Hash(r.BeforeObjects) != Hash(r.AfterObjects) || Hash(mutationMultiset(before.Mutations)) != Hash(mutationMultiset(after.Mutations)) {
		r.Status = "failed"
		r.Reason = "object mutations or final inventory changed"
	}
	return r
}
