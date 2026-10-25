package main_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

// adr: 20261019102657 — without FAAS_REALTIME_PREVIEW_ACCOUNTS in the shell
// the realtime preview's verbs, retained delivery and gregale.yaml section
// are absent from help, the published reference and dispatch.
func TestRealtimePreviewHiddenFromCLI(t *testing.T) {
	realtimeSubs := func() (names []string, delivery []string) {
		for _, command := range main.VisibleCliCommands() {
			if command.Name != "realtime" {
				continue
			}
			for _, sub := range command.Subcommands {
				names = append(names, sub.Name)
				if sub.Name == "publish" {
					for _, f := range sub.Flags {
						if f.Name == "delivery" {
							delivery = f.ClosedSet
						}
					}
				}
			}
		}
		return names, delivery
	}

	t.Setenv("FAAS_REALTIME_PREVIEW_ACCOUNTS", "")
	names, delivery := realtimeSubs()
	for _, verb := range main.RealtimePreviewVerbs {
		if slices.Contains(names, verb) {
			t.Errorf("help lists realtime %s", verb)
		}
		if strings.Contains(main.RealtimeUsage(), "|"+verb+"|") {
			t.Errorf("usage lists realtime %s", verb)
		}
	}
	if slices.Contains(delivery, "retained") || strings.Contains(main.RealtimePublishUsage(), "retained") {
		t.Errorf("publish offers retained delivery: %v", delivery)
	}
	if !slices.Contains(names, "publish") || !slices.Contains(names, "connections") {
		t.Fatalf("hiding removed released verbs: %v", names)
	}
	if code := main.Run([]string{"realtime", "occupancy", "app", "endpoint", "lobby"}); code == 0 {
		t.Error("a hidden verb ran")
	}
	reference, err := os.ReadFile("../../docs/cli-reference.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(reference), "realtime occupancy") || strings.Contains(string(reference), "live|retained") {
		t.Error("the published CLI reference documents the realtime preview")
	}
	m := &gregalemanifest.Manifest{Realtime: &gregalemanifest.RealtimeConfig{}}
	if _, err := main.ReconcileManifestRealtimeEndpoints(context.Background(), nil, "app", m, &main.ManifestCronTransaction{}); !errors.Is(err, main.ErrRealtimeManifestHidden) {
		t.Errorf("gregale.yaml realtime section = %v, want the unknown-key error", err)
	}

	t.Setenv("FAAS_REALTIME_PREVIEW_ACCOUNTS", "*")
	names, delivery = realtimeSubs()
	if !slices.Contains(names, "occupancy") || !slices.Contains(delivery, "retained") {
		t.Errorf("preview shell hides the preview: verbs %v, delivery %v", names, delivery)
	}
}
