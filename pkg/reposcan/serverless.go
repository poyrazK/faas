package reposcan

import (
	"fmt"
	"io/fs"
	"sort"
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
// One function with multiple schedule events is unusual but
// supported — we emit one workload per function and merge their
// schedules into a comma-joined string (Phase 3's crons-row
// creator splits on comma).
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
	// The schedule object has `rate` or `cron` fields; both
	// are valid cron expressions ("rate(5 minutes)" or
	// "cron(0 12 * * * *)"). We keep the string form verbatim.
}
type serverlessHTTP struct {
	Path string `yaml:"path"`
}
type scheduleRate struct {
	Rate string `yaml:"rate"`
	Cron string `yaml:"cron"`
}

var slsFileNames = []string{nameServerlessYML, nameServerlessYAML}

func detectServerless(fsys fs.FS) ([]workloadSeed, []Managed, []string, error) {
	body, src, err := readFirstValidFile(fsys, slsFileNames)
	if err != nil || body == nil {
		return nil, nil, nil, err
	}
	var d serverlessDoc
	if err := yaml.Unmarshal(body, &d); err != nil {
		// Warn-and-skip: malformed serverless.yml is recoverable.
		return nil, nil, []string{"reposcan: parse " + src + ": " + err.Error()}, nil //nolint:nilerr
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
		cls := ClassUnknown
		var schedules []string
		for _, e := range fn.Events {
			switch {
			case e.HTTP != nil, e.HTTPApi != nil:
				if cls != ClassHTTP {
					cls = ClassHTTP
				}
			case e.Schedule != nil:
				cls = ClassJob
				if e.Schedule.Cron != "" {
					schedules = append(schedules, e.Schedule.Cron)
				} else if e.Schedule.Rate != "" {
					schedules = append(schedules, e.Schedule.Rate)
				}
			}
		}
		s := workloadSeed{
			name:   n,
			class:  cls,
			source: src + ": " + n,
		}
		if len(schedules) > 0 {
			s.schedule = schedules[0] // primary schedule
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
