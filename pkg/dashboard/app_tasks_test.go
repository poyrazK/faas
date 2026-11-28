package dashboard

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAppTasksPageRendersHistoryAndSelectedTask(t *testing.T) {
	exit := 3
	started, finished := "2026-09-23T00:00:00Z", "2026-09-23T00:00:02Z"
	task := api.AppTaskResponse{ID: "2bdd4251-f567-4a48-9f66-a155bbfa7751", Kind: api.AppTaskKindRelease, Status: api.AppTaskStatusFailed,
		Command: []string{"bin/migrate", "<up>"}, ExitCode: &exit, StartedAt: &started, FinishedAt: &finished, CreatedAt: started,
		StdoutTail: "applied <script>\n", Failure: &api.AppTaskFailure{Code: "process_exit", Message: "command exited unsuccessfully"}}
	render := func(data AppTasksData) string {
		t.Helper()
		w := httptest.NewRecorder()
		if err := Render(w, slog.New(slog.NewTextHandler(io.Discard, nil)), "test-nonce", Page{Title: "Tasks", Body: "app_tasks", Data: data}); err != nil {
			t.Fatal(err)
		}
		return w.Body.String()
	}
	row := NewAppTaskRow(task)
	list := render(AppTasksData{Slug: "orders", Enabled: true, Tasks: []AppTaskRow{row}, HasNext: true, NextOffset: 25})
	for _, want := range []string{"?task=2bdd4251-f567-4a48-9f66-a155bbfa7751", "release", "2s", "bin/migrate &#39;&lt;up&gt;&#39;", "?offset=25", "2026-09-23 00:00:00 UTC"} {
		if !strings.Contains(list, want) {
			t.Errorf("history page missing %q", want)
		}
	}
	detail := render(AppTasksData{Slug: "orders", Enabled: true, Selected: &row})
	for _, want := range []string{"process_exit: command exited unsuccessfully", "applied &lt;script&gt;", "All tasks"} {
		if !strings.Contains(detail, want) {
			t.Errorf("task page missing %q", want)
		}
	}
	if strings.Contains(detail, "<script>") {
		t.Error("task output must be escaped")
	}
	if off := render(AppTasksData{Slug: "orders"}); !strings.Contains(off, "not enabled on this host") {
		t.Error("disabled page should say app tasks are not enabled")
	}
}
