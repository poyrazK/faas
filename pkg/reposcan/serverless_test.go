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
		{"nightly", ClassJob, "rate(5 minutes)"},
		{"hourly", ClassJob, "cron(0 * * * ? *)"},
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
