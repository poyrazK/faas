package main_test

import (
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
	previous := *main.JsonOutput
	*main.JsonOutput = true
	t.Cleanup(func() { *main.JsonOutput = previous })
	out, stderr, restore := main.SwapIO(t)
	calls := 0
	code := main.RunStreamList(context.Background(), "Invalid config", "", func(context.Context, string) ([]int, string, error) {
		calls++
		if calls == 1 {
			return []int{1, 2}, "next", nil
		}
		return nil, "", inputErr
	})
	text := stderr()
	restore()
	progress := main.ListStreamProgress{PagesCompleted: 1, ItemsEmitted: 2, Partial: true}
	var problem struct {
		inputProblemView
		Pagination main.ListStreamProgress `json:"pagination"`
	}
	if code != 1 || calls != 2 || out.String() != "1\n2\n" || json.Unmarshal([]byte(text), &problem) != nil || !reflect.DeepEqual(problem.Pagination, progress) || problem.CLI == nil || problem.CLI.Kind != "invalid_json" || problem.CLI.Flag != "--config" || problem.CLI.Location == nil || problem.CLI.Location.Column != 2 {
		t.Fatalf("input location or stream progress lost: exit=%d stdout=%s stderr=%s", code, out, text)
	}
}
