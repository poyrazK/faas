// adr: 642
package oci

import (
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestComposeHealthcheckMergesImageDefaults(t *testing.T) {
	base := ImageConfig{Cmd: []string{"/server"}, Healthcheck: &ImageHealthcheck{Test: []string{"CMD", "/image-check"},
		Retries: 4, ImageTiming: &api.OCIHealthcheckTiming{IntervalNS: int64(20 * time.Second),
			TimeoutNS: int64(5 * time.Second), StartPeriodNS: int64(time.Second), StartIntervalNS: int64(100 * time.Millisecond)}}}
	for _, tc := range []struct {
		name  string
		check *api.ComposeHealthcheck
		want  []string
	}{
		{"inherit", nil, base.Healthcheck.Test},
		{"empty", &api.ComposeHealthcheck{}, base.Healthcheck.Test},
		{"replace", &api.ComposeHealthcheck{Test: []string{"CMD-SHELL", "exit 0"}}, []string{"CMD-SHELL", "exit 0"}},
		{"disable", &api.ComposeHealthcheck{Test: []string{"NONE"}}, []string{"NONE"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config, err := ApplyComposeHealthcheck(base, tc.check)
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := ManifestFromConfig(Config{Cmd: config.Cmd, Healthcheck: config.Healthcheck})
			if err != nil || !reflect.DeepEqual(manifest.Healthcheck.Test, tc.want) || manifest.Healthcheck.Retries != 4 ||
				!reflect.DeepEqual(manifest.Healthcheck.ImageTiming, base.Healthcheck.ImageTiming) {
				t.Fatalf("merged manifest = %+v, %v", manifest.Healthcheck, err)
			}
		})
	}
	config, err := ApplyComposeHealthcheck(base, &api.ComposeHealthcheck{IntervalNS: int64(1500 * time.Millisecond), Retries: 2})
	if err != nil || config.Healthcheck.ImageTiming.IntervalNS != int64(1500*time.Millisecond) || config.Healthcheck.Retries != 2 ||
		config.Healthcheck.ImageTiming.TimeoutNS != base.Healthcheck.ImageTiming.TimeoutNS {
		t.Fatalf("partial override = %+v, %v", config.Healthcheck, err)
	}
	config.Healthcheck.Test[0] = "changed"
	config.Healthcheck.ImageTiming.TimeoutNS = 1
	if base.Healthcheck.Test[0] != "CMD" || base.Healthcheck.ImageTiming.TimeoutNS != int64(5*time.Second) {
		t.Fatal("merge mutated image metadata")
	}
}

func TestComposeHealthcheckNewAndLegacyImageChecks(t *testing.T) {
	config, err := ApplyComposeHealthcheck(ImageConfig{Cmd: []string{"/server"}}, &api.ComposeHealthcheck{
		Test: []string{"CMD", "/check"}, TimeoutNS: int64(250 * time.Millisecond)})
	if err != nil || config.Healthcheck.ImageTiming.TimeoutNS != int64(250*time.Millisecond) {
		t.Fatalf("new healthcheck = %+v, %v", config.Healthcheck, err)
	}
	legacy := ImageConfig{Cmd: []string{"/server"}, Healthcheck: &ImageHealthcheck{Test: []string{"CMD", "/check"}, IntervalS: 15, TimeoutS: 2, StartPeriodS: 3}}
	config, err = ApplyComposeHealthcheck(legacy, &api.ComposeHealthcheck{Retries: 2})
	if err != nil || config.Healthcheck.ImageTiming.IntervalNS != int64(15*time.Second) || config.Healthcheck.ImageTiming.TimeoutNS != int64(2*time.Second) || config.Healthcheck.ImageTiming.StartPeriodNS != int64(3*time.Second) {
		t.Fatalf("legacy timing = %+v, %v", config.Healthcheck, err)
	}
	if _, err := ApplyComposeHealthcheck(legacy, &api.ComposeHealthcheck{Test: []string{"invalid"}}); err == nil {
		t.Fatal("invalid persisted override was accepted")
	}
}
