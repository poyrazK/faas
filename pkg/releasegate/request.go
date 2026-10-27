package releasegate

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

var (
	gateNamePattern   = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
	metricNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
)

// Spec is a validated create request; SLOName still has to be resolved to
// the app's SLO.
type Spec struct {
	Name         string
	Kind         string
	SLOName      string
	MaxBurnRate  int
	MaxNewGroups int
	MinEvents    int
	MetricName   string
	Comparison   string
	Threshold    float64
	MinSamples   int
}

// FromRequest validates a create request and fills defaults.
func FromRequest(req api.CreateReleaseGateRequest) (Spec, *api.Problem) {
	if !gateNamePattern.MatchString(req.Name) {
		return Spec{}, api.ErrValidation("name must be 1-63 lowercase letters, digits, '-' or '_', starting with a letter")
	}
	s := Spec{Name: req.Name, Kind: req.Kind, MinSamples: req.MinSamples}
	if s.MinSamples == 0 {
		s.MinSamples = api.ReleaseGateDefaultMinSamples
	}
	if s.MinSamples < 1 || s.MinSamples > 1000000 {
		return Spec{}, api.ErrValidation("min_samples must be 1-1000000")
	}
	sloFields := req.SLO != "" || req.MaxBurnRate != 0
	errorFields := req.MaxNewGroups != nil || req.MinEvents != 0
	metricFields := req.Metric != "" || req.Comparison != "" || req.Threshold != nil
	aiFields := req.MaxIncreasePercent != 0
	switch req.Kind {
	case KindSLO:
		if errorFields || metricFields || aiFields {
			return Spec{}, api.ErrValidation("an slo gate takes only slo, max_burn_rate and min_samples")
		}
		if req.SLO == "" {
			return Spec{}, api.ErrValidation("an slo gate needs slo, the name of one of the app's SLOs")
		}
		s.SLOName, s.MaxBurnRate = req.SLO, req.MaxBurnRate
		if s.MaxBurnRate == 0 {
			s.MaxBurnRate = api.ReleaseGateDefaultMaxBurnRate
		}
		if s.MaxBurnRate < 1 || s.MaxBurnRate > 1000 {
			return Spec{}, api.ErrValidation("max_burn_rate must be 1-1000")
		}
	case KindNewErrors:
		if sloFields || metricFields || aiFields {
			return Spec{}, api.ErrValidation("a new_errors gate takes only max_new_groups, min_events and min_samples")
		}
		s.MaxNewGroups = api.ReleaseGateDefaultMaxNewGroups
		if req.MaxNewGroups != nil {
			s.MaxNewGroups = *req.MaxNewGroups
		}
		s.MinEvents = req.MinEvents
		if s.MinEvents == 0 {
			s.MinEvents = api.ReleaseGateDefaultMinEvents
		}
		if s.MaxNewGroups < 0 || s.MaxNewGroups > 100 {
			return Spec{}, api.ErrValidation("max_new_groups must be 0-100")
		}
		if s.MinEvents < 1 || s.MinEvents > 100000 {
			return Spec{}, api.ErrValidation("min_events must be 1-100000")
		}
	case KindCustomMetric:
		if sloFields || errorFields || aiFields {
			return Spec{}, api.ErrValidation("a custom_metric gate takes only metric, comparison, threshold and min_samples")
		}
		if !metricNamePattern.MatchString(req.Metric) {
			return Spec{}, api.ErrValidation("metric must be a custom metric name: lowercase letters, digits and '_', starting with a letter")
		}
		if req.Comparison != "below" && req.Comparison != "above" {
			return Spec{}, api.ErrValidation("comparison must be below or above")
		}
		if req.Threshold == nil || math.IsNaN(*req.Threshold) || math.IsInf(*req.Threshold, 0) {
			return Spec{}, api.ErrValidation("a custom_metric gate needs a finite threshold")
		}
		s.MetricName, s.Comparison, s.Threshold = req.Metric, req.Comparison, *req.Threshold
	case KindAI:
		if sloFields || errorFields || req.Comparison != "" || req.Threshold != nil {
			return Spec{}, api.ErrValidation("an ai gate takes only metric, max_increase_percent and min_samples")
		}
		if !slices.Contains(AIMetrics, req.Metric) {
			return Spec{}, api.ErrValidation("an ai gate's metric must be " + strings.Join(AIMetrics, ", "))
		}
		increase := req.MaxIncreasePercent
		if increase == 0 {
			increase = api.ReleaseGateAIDefaultMaxIncrease
		}
		if increase < api.ReleaseGateAIMaxIncreaseMin || increase > api.ReleaseGateAIMaxIncreaseMax {
			return Spec{}, api.ErrValidation(fmt.Sprintf("max_increase_percent must be %d-%d", api.ReleaseGateAIMaxIncreaseMin, api.ReleaseGateAIMaxIncreaseMax))
		}
		s.MetricName, s.Threshold = req.Metric, float64(increase)
	default:
		return Spec{}, api.ErrValidation(fmt.Sprintf("kind must be %s, %s, %s or %s", KindSLO, KindNewErrors, KindCustomMetric, KindAI))
	}
	return s, nil
}
