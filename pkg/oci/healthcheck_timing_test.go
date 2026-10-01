package oci

import (
	"strings"
	"testing"
	"time"
)

func TestHealthcheckImageNanosecondTimingSurvivesManifest(t *testing.T) {
	input := `{"architecture":"amd64","os":"linux","config":{"Cmd":["/server"],"Healthcheck":{"Test":["CMD","/check"],"Interval":1500000000,"Timeout":250000000,"StartPeriod":750000000,"StartInterval":50000000,"Retries":2}}}`
	cfg, err := ParseConfig(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := parseImageConfig([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []*ImageHealthcheck{cfg.Healthcheck, registry.Healthcheck} {
		if check == nil || check.ImageTiming == nil {
			t.Fatal("exact image timing missing")
		}
		timing := check.ImageTiming
		if timing.IntervalNS != int64(1500*time.Millisecond) || timing.TimeoutNS != int64(250*time.Millisecond) || timing.StartPeriodNS != int64(750*time.Millisecond) || timing.StartIntervalNS != int64(50*time.Millisecond) {
			t.Fatalf("image timing lost precision: %+v", timing)
		}
		if check.IntervalS != 2 || check.TimeoutS != 1 || check.StartPeriodS != 1 {
			t.Fatalf("incorrect compatibility timing view: %+v", check)
		}
	}
	manifest, err := ManifestFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Healthcheck == nil || manifest.Healthcheck.ImageTiming == nil || *manifest.Healthcheck.ImageTiming != *cfg.Healthcheck.ImageTiming {
		t.Fatalf("manifest lost exact image timing: %+v", manifest.Healthcheck)
	}
}

func TestHealthcheckRejectsInvalidImageTiming(t *testing.T) {
	for _, field := range []string{"Interval", "Timeout", "StartPeriod", "StartInterval"} {
		for _, value := range []string{"-1", "3", "0.5", "9223372036854775808"} {
			input := `{"config":{"Healthcheck":{"Test":["CMD","/check"],"` + field + `":` + value + `}}}`
			if _, err := ParseConfig(strings.NewReader(input)); err == nil {
				t.Errorf("accepted invalid %s=%s", field, value)
			}
			if _, err := parseImageConfig([]byte(input)); err == nil {
				t.Errorf("registry parser accepted invalid %s=%s", field, value)
			}
		}
	}
	if _, err := ParseConfig(strings.NewReader(`{"config":{"Healthcheck":{"Retries":-1}}}`)); err == nil {
		t.Fatal("negative retry count accepted")
	}
}
