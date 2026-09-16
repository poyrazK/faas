package reposcan

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestDetectServerless_FunctionsAndScheduleEvents(t *testing.T) {
	t.Parallel()
	body := `service: my-app
provider:
  name: aws
functions:
  api:
    handler: handler.api
    events:
      - http:
          path: /users
          method: get
  nightly:
    handler: handler.nightly
    events:
      - schedule:
          rate: rate(5 minutes)
  hourly:
    handler: handler.hourly
    events:
      - schedule:
          cron: "cron(0 * * * ? *)"
`
	fsys := fstest.MapFS{
		"serverless.yml": &fstest.MapFile{Data: []byte(body)},
	}
	seeds, _, warnings, err := detectServerless(fsys)
	if err != nil {
		t.Fatalf("detectServerless: %v", err)
	}
	byName := map[string]workloadSeed{}
	for _, s := range seeds {
		byName[s.name] = s
	}
	cases := []struct {
		name string
		cls  Class
		sch  string
	}{
		{"api", ClassHTTP, ""},
		{"nightly", ClassJob, "*/5 * * * *"},
		{"hourly", ClassJob, "0 * * * *"},
	}
	for _, c := range cases {
		s, ok := byName[c.name]
		if !ok {
			t.Errorf("function %q missing", c.name)
			continue
		}
		if s.class != c.cls {
			t.Errorf("function %q class = %q, want %q", c.name, s.class, c.cls)
		}
		if s.schedule != c.sch {
			t.Errorf("function %q schedule = %q, want %q", c.name, s.schedule, c.sch)
		}
	}
	if len(warnings) != len(cases) {
		t.Fatalf("warnings = %d, want %d", len(warnings), len(cases))
	}
	for _, warning := range warnings {
		if !strings.Contains(warning, "not applicable to project apply") ||
			!strings.Contains(warning, "no execution adapter") {
			t.Errorf("warning = %q, want an explicit migration instruction", warning)
		}
	}
}

func TestDetectServerless_AbsentFile(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{"Dockerfile": &fstest.MapFile{Data: []byte("FROM scratch")}}
	seeds, _, _, err := detectServerless(fsys)
	if err != nil {
		t.Fatalf("detectServerless: %v", err)
	}
	if len(seeds) != 0 {
		t.Errorf("seeds = %v, want empty", names(seeds))
	}
}

func TestDetectServerless_MixedEventsAreOrderIndependentAndKeepEverySchedule(t *testing.T) {
	t.Parallel()
	variants := []string{
		`functions:
  api:
    handler: handler.api
    events:
      - http: {path: /}
      - schedule: {rate: "rate(5 minutes)"}
      - schedule: {cron: "cron(0 12 * * ? *)", enabled: false}
`,
		`functions:
  api:
    handler: handler.api
    events:
      - schedule: {cron: "cron(0 12 * * ? *)", enabled: false}
      - schedule: {rate: "rate(5 minutes)"}
      - http: {path: /}
`,
	}
	for i, body := range variants {
		seeds, _, _, err := detectServerless(fstest.MapFS{
			"serverless.yml": &fstest.MapFile{Data: []byte(body)},
		})
		if err != nil {
			t.Fatalf("variant %d: %v", i, err)
		}
		if len(seeds) != 1 || seeds[0].class != ClassHTTP {
			t.Fatalf("variant %d: seeds = %#v, want one HTTP workload", i, seeds)
		}
		if len(seeds[0].schedules) != 2 {
			t.Fatalf("variant %d: schedules = %#v, want 2", i, seeds[0].schedules)
		}
		got := map[string]bool{}
		for _, schedule := range seeds[0].schedules {
			got[schedule.Expression] = schedule.Enabled
		}
		if enabled, ok := got["*/5 * * * *"]; !ok || !enabled {
			t.Errorf("variant %d: rate schedule = %#v", i, got)
		}
		if enabled, ok := got["0 12 * * *"]; !ok || enabled {
			t.Errorf("variant %d: disabled cron schedule = %#v", i, got)
		}
	}
}

func TestNormalizeServerlessSchedule_RejectsInexactCadence(t *testing.T) {
	t.Parallel()
	if _, err := normalizeServerlessSchedule("rate(7 minutes)"); err == nil {
		t.Fatal("rate(7 minutes) must fail: five-field cron cannot represent it exactly")
	}
	if _, err := normalizeServerlessSchedule("cron(0 12 * * ? 2027)"); err == nil {
		t.Fatal("year-constrained AWS cron must fail")
	}
}
