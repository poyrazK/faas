package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func TestJSONInputLocationRetainsStreamProgress(t *testing.T) {
	setupJSONHelp(t)
	_, inputErr := main.ResolveJSONFlag("--config", "{")
	if inputErr == nil {
		t.Fatal("invalid JSON parsed")
	}
	var streamed bytes.Buffer
	calls := 0
	progress, err := main.StreamListPages(context.Background(), &streamed, "", func(context.Context, string) ([]int, string, error) {
		calls++
		if calls == 1 {
			return []int{1, 2}, "next", nil
		}
		return nil, "", inputErr
	})
	if err == nil || progress.PagesCompleted != 1 || progress.ItemsEmitted != 2 || !progress.Partial {
		t.Fatal("stream did not retain its completed records", progress, err)
	}
	previous := *main.JsonOutput
	*main.JsonOutput = true
	t.Cleanup(func() { *main.JsonOutput = previous })
	out, stderr, restore := main.SwapIO(t)
	code := main.PrintErr("Invalid config", err)
	text := stderr()
	restore()
	var problem struct {
		inputProblemView
		Pagination main.ListStreamProgress `json:"pagination"`
	}
	if code != 1 || out.Len() != 0 || json.Unmarshal([]byte(text), &problem) != nil || !reflect.DeepEqual(problem.Pagination, progress) || problem.CLI == nil || problem.CLI.Kind != "invalid_json" || problem.CLI.Flag != "--config" || problem.CLI.Location == nil || problem.CLI.Location.Column != 2 {
		t.Fatalf("input location or stream progress lost: exit=%d stdout=%s stderr=%s", code, out, text)
	}
}
