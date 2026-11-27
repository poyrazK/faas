package preview

// Rule experiments: layer slices, the environment holdout and subject
// exposures (ADR 20261119173702).

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"time"
)

const (
	flagHoldoutMaxBasisPoints = 2000
	flagLayersMax             = 10
	flagExposureBatch         = 100
	flagExposureQueueMax      = 1000
	flagExposureDedupe        = time.Hour
	flagExposureDedupeMax     = 10_000
)

type runtimeHoldout struct {
	seed        string
	basisPoints int
}

type runtimeRuleExperiment struct {
	key, control, unit, layer string
	from, to                  int
}

type wireHoldout struct {
	Seed        string `json:"seed"`
	BasisPoints *int   `json:"basis_points"`
}

type wireLayer struct {
	Key  string `json:"key"`
	Seed string `json:"seed"`
}

type wireRuleExperiment struct {
	Key     string `json:"key"`
	Control string `json:"control"`
	Unit    string `json:"unit"`
	Layer   string `json:"layer"`
	From    int    `json:"from"`
	To      int    `json:"to"`
}

func validFlagSeed(seed string) bool { return seed != "" && len(seed) <= 128 }

func decodeFlagHoldout(raw json.RawMessage) (*runtimeHoldout, error) {
	if len(raw) == 0 || isJSONNull(raw) {
		return nil, nil
	}
	var h wireHoldout
	if json.Unmarshal(raw, &h) != nil || !validFlagSeed(h.Seed) || h.BasisPoints == nil || *h.BasisPoints < 0 || *h.BasisPoints > flagHoldoutMaxBasisPoints {
		return nil, errors.New("invalid Flags holdout")
	}
	return &runtimeHoldout{seed: h.Seed, basisPoints: *h.BasisPoints}, nil
}

func decodeFlagLayers(raw json.RawMessage) (map[string]string, error) {
	if len(raw) == 0 || isJSONNull(raw) {
		return nil, nil
	}
	var layers []wireLayer
	if json.Unmarshal(raw, &layers) != nil || len(layers) > flagLayersMax {
		return nil, errors.New("invalid Flags layers")
	}
	out := make(map[string]string, len(layers))
	for _, l := range layers {
		if _, dup := out[l.Key]; dup || !validFlagKey(l.Key) || !validFlagSeed(l.Seed) {
			return nil, errors.New("invalid Flags layer")
		}
		out[l.Key] = l.Seed
	}
	return out, nil
}

func decodeRuleExperiment(raw json.RawMessage, flagType string, rule runtimeFlagRule, variants map[string]struct{}, layers map[string]string) (*runtimeRuleExperiment, error) {
	if len(raw) == 0 || isJSONNull(raw) {
		return nil, nil
	}
	var w wireRuleExperiment
	if json.Unmarshal(raw, &w) != nil || flagType != "variant" || rule.valueSet || !flagExperimentKey.MatchString(w.Key) {
		return nil, errors.New("invalid Flags rule experiment")
	}
	if _, ok := variants[w.Control]; !ok {
		return nil, errors.New("Flags rule experiment control is not a variant")
	}
	if (w.Unit != "customer" && w.Unit != "subject") || (w.Unit == "subject") != (rule.rolloutUnit == "subject") {
		return nil, errors.New("invalid Flags rule experiment unit")
	}
	if w.Layer == "" && (w.From != 0 || w.To != 0) {
		return nil, errors.New("invalid Flags rule experiment slice")
	}
	if w.Layer != "" {
		if _, ok := layers[w.Layer]; !ok || w.From < 0 || w.From >= w.To || w.To > 10_000 {
			return nil, errors.New("invalid Flags rule experiment layer")
		}
	}
	return &runtimeRuleExperiment{key: w.Key, control: w.Control, unit: w.Unit, layer: w.Layer, from: w.From, to: w.To}, nil
}

func flagBucketOf(parts ...string) int {
	h := sha256.New()
	for i, p := range parts {
		if i > 0 {
			h.Write([]byte{0})
		}
		h.Write([]byte(p))
	}
	sum := h.Sum(nil)
	return int(binary.BigEndian.Uint32(sum[:4]) % 10_000)
}

// experimentGate applies a rule's experiment: whether the unit is in the
// layer slice, and whether the environment holdout holds the customer out.
func (b *runtimeFlagsBundle) experimentGate(x *runtimeRuleExperiment, customer, subject string) (inSlice, held bool) {
	if x.layer != "" {
		seed := b.layers[x.layer]
		bucket := flagBucketOf(seed, "layer", customer)
		if x.unit == "subject" {
			bucket = flagBucketOf(seed, "layer", customer, subject)
		}
		if bucket < x.from || bucket >= x.to {
			return false, false
		}
	}
	h := b.holdout
	return true, h != nil && h.basisPoints > 0 && flagBucketOf(h.seed, "holdout", customer) < h.basisPoints
}

// subjectExperiment reports whether a flag's rule carries a subject
// experiment, whose exposures the SDK reports.
func (b *runtimeFlagsBundle) subjectExperiment(key, ruleID string) bool {
	flag, ok := b.flags[key]
	if !ok {
		return false
	}
	for _, rule := range flag.rules {
		if rule.id == ruleID {
			return rule.experiment != nil && rule.experiment.unit == "subject"
		}
	}
	return false
}

type flagExposure struct {
	Flag       string `json:"flag"`
	RuleID     string `json:"rule_id"`
	Variant    string `json:"variant"`
	CustomerID string `json:"customer_id"`
	Subject    string `json:"subject"`
	At         string `json:"at"`
}

// recordExposure queues one subject exposure, at most once an hour per
// flag, customer, subject and variant.
func (f *GregaleFlags) recordExposure(decision FlagDecision, customer, subject string, now time.Time) {
	value, _ := decision.Value.(string)
	key := decision.Flag + "\x00" + customer + "\x00" + subject + "\x00" + value
	q := &f.events
	q.mu.Lock()
	defer q.mu.Unlock()
	if seen, ok := q.exposed[key]; ok && now.Sub(seen) < flagExposureDedupe {
		return
	}
	if q.exposed == nil || len(q.exposed) >= flagExposureDedupeMax {
		q.exposed = make(map[string]time.Time)
	}
	if len(q.exposures) >= flagExposureQueueMax {
		q.dropped++
		return
	}
	q.exposed[key] = now
	q.exposures = append(q.exposures, flagExposure{Flag: decision.Flag, RuleID: decision.RuleID, Variant: value, CustomerID: customer, Subject: subject, At: now.UTC().Format(time.RFC3339Nano)})
}
