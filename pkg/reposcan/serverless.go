package reposcan

import (
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// serverlessDoc is a subset of the Serverless Framework v1.x
// format. We read `provider` (no-op), `functions: <name>:
// handler|events`, and treat each event as a class hint:
//
//	events[].http            → class=http (apiGw-http in v3)
//	events[].schedule        → class=job + .schedule rate
//	events[].httpApi         → class=http
//
// One function may declare multiple schedule events. We retain every schedule
// in its desired set while keeping the first as the legacy primary field.
type serverlessDoc struct {
	Functions map[string]serverlessFunction `yaml:"functions"`
}
type serverlessFunction struct {
	Handler string            `yaml:"handler"`
	Events  []serverlessEvent `yaml:"events"`
}
type serverlessEvent struct {
	HTTP     *serverlessHTTP `yaml:"http"`
	HTTPApi  *serverlessHTTP `yaml:"httpApi"`
	Schedule *scheduleRate   `yaml:"schedule"`
	// The schedule object has `rate` or `cron` fields. Exact EventBridge
	// cadences are normalized to Gregale's five-field grammar below.
}
type serverlessHTTP struct {
	Path string `yaml:"path"`
}
type scheduleRate struct {
	Rate    string `yaml:"rate"`
	Cron    string `yaml:"cron"`
	Enabled *bool  `yaml:"enabled"`
}

// UnmarshalYAML accepts both Serverless schedule forms:
// `schedule: rate(5 minutes)` and `schedule: {rate: ..., enabled: false}`.
func (s *scheduleRate) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		s.Rate = node.Value
		return nil
	}
	type rawScheduleRate scheduleRate
	var raw rawScheduleRate
	if err := node.Decode(&raw); err != nil {
		return err
	}
	*s = scheduleRate(raw)
	return nil
}

var slsFileNames = []string{nameServerlessYML, nameServerlessYAML}

func detectServerless(fsys fs.FS) ([]workloadSeed, []Managed, []string, error) {
	body, src, err := readFirstValidFile(fsys, slsFileNames)
	if err != nil || body == nil {
		return nil, nil, nil, err
	}
	var d serverlessDoc
	if err := yaml.Unmarshal(body, &d); err != nil {
		return nil, nil, nil, fmt.Errorf("reposcan: parse %s: %w", src, err)
	}
	var seeds []workloadSeed
	names := make([]string, 0, len(d.Functions))
	for n := range d.Functions {
		if n != "" {
			names = append(names, n)
		}
	}
	sort.Strings(names)

	warnings := make([]string, 0, len(names))
	for _, n := range names {
		fn := d.Functions[n]
		hasHTTP := false
		var schedules []CronSchedule
		for _, e := range fn.Events {
			switch {
			case e.HTTP != nil, e.HTTPApi != nil:
				hasHTTP = true
			case e.Schedule != nil:
				raw := e.Schedule.Rate
				if e.Schedule.Cron != "" {
					raw = e.Schedule.Cron
				}
				if strings.TrimSpace(raw) == "" {
					continue
				}
				expression, err := normalizeServerlessSchedule(raw)
				if err != nil {
					return nil, nil, nil, fmt.Errorf("parse %s function %q schedule %q: %w", src, n, raw, err)
				}
				enabled := true
				if e.Schedule.Enabled != nil {
					enabled = *e.Schedule.Enabled
				}
				candidate := CronSchedule{Expression: expression, Enabled: enabled}
				if !containsCronSchedule(schedules, candidate) {
					schedules = append(schedules, candidate)
				}
			}
		}
		cls := ClassUnknown
		if hasHTTP {
			cls = ClassHTTP
		} else if len(schedules) > 0 {
			cls = ClassJob
		}
		s := workloadSeed{
			name:      n,
			class:     cls,
			source:    src + ": " + n,
			schedules: schedules,
		}
		if len(schedules) > 0 {
			s.schedule = schedules[0].Expression
		}
		seeds = append(seeds, s)
		handler := strings.TrimSpace(fn.Handler)
		if handler == "" {
			warnings = append(warnings, fmt.Sprintf(
				"reposcan: %s: function %q is not applicable to project apply: no Serverless handler was declared; create a function app and deploy its handler explicitly",
				src, n))
		} else {
			warnings = append(warnings, fmt.Sprintf(
				"reposcan: %s: function %q is not applicable to project apply: Serverless handler %q has no execution adapter; create a function app and deploy the handler explicitly",
				src, n, handler))
		}
	}
	return seeds, nil, warnings, nil
}

func containsCronSchedule(schedules []CronSchedule, want CronSchedule) bool {
	for _, schedule := range schedules {
		if schedule == want {
			return true
		}
	}
	return false
}

// normalizeServerlessSchedule converts the subset of EventBridge schedule
// syntax that has an exact five-field Gregale equivalent. Expressions whose
// cadence cannot be represented exactly fail closed.
func normalizeServerlessSchedule(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "rate(") && strings.HasSuffix(raw, ")") {
		parts := strings.Fields(strings.TrimSpace(raw[len("rate(") : len(raw)-1]))
		if len(parts) != 2 {
			return "", fmt.Errorf("unsupported rate expression")
		}
		n, err := strconv.Atoi(parts[0])
		if err != nil || n <= 0 {
			return "", fmt.Errorf("rate interval must be a positive integer")
		}
		unit := strings.TrimSuffix(strings.ToLower(parts[1]), "s")
		switch unit {
		case "minute":
			if n == 1 {
				return "* * * * *", nil
			}
			if n < 60 && 60%n == 0 {
				return fmt.Sprintf("*/%d * * * *", n), nil
			}
			if n == 60 {
				return "0 * * * *", nil
			}
		case "hour":
			if n == 1 {
				return "0 * * * *", nil
			}
			if n < 24 && 24%n == 0 {
				return fmt.Sprintf("0 */%d * * *", n), nil
			}
		case "day":
			if n == 1 {
				return "0 0 * * *", nil
			}
		}
		return "", fmt.Errorf("rate cadence has no exact five-field cron equivalent")
	}
	if strings.HasPrefix(raw, "cron(") && strings.HasSuffix(raw, ")") {
		parts := strings.Fields(strings.TrimSpace(raw[len("cron(") : len(raw)-1]))
		if len(parts) != 6 {
			return "", fmt.Errorf("AWS cron must contain minute, hour, day-of-month, month, day-of-week, and year")
		}
		if parts[5] != "*" {
			return "", fmt.Errorf("year-constrained AWS cron has no five-field equivalent")
		}
		for i := 0; i < 5; i++ {
			if strings.ContainsAny(parts[i], "LW#") {
				return "", fmt.Errorf("AWS-specific L, W, and # operators are unsupported")
			}
		}
		if parts[2] == "?" {
			parts[2] = "*"
		}
		if parts[4] == "?" {
			parts[4] = "*"
		}
		if parts[4] != "*" && strings.IndexFunc(parts[4], func(r rune) bool { return r >= '0' && r <= '9' }) >= 0 {
			return "", fmt.Errorf("numeric AWS day-of-week values are ambiguous; use SUN-SAT names")
		}
		return strings.Join(parts[:5], " "), nil
	}
	return raw, nil
}
