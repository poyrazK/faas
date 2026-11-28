package apptaskdashboard_test

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apptaskdashboard"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

const taskID = "2bdd4251-f567-4a48-9f66-a155bbfa7751"

type fakeStore struct {
	tasks   []state.AppTask
	getErr  error
	listErr error
	offsets []int
}

func (f *fakeStore) AppTaskByID(_ context.Context, _, _, id string) (state.AppTask, error) {
	if f.getErr != nil {
		return state.AppTask{}, f.getErr
	}
	for _, task := range f.tasks {
		if task.ID == id {
			return task, nil
		}
	}
	return state.AppTask{}, state.ErrNotFound
}

func (f *fakeStore) ListAppTasks(_ context.Context, _, _ string, limit, offset int) ([]state.AppTask, error) {
	f.offsets = append(f.offsets, offset)
	if f.listErr != nil {
		return nil, f.listErr
	}
	if offset >= len(f.tasks) {
		return nil, nil
	}
	return f.tasks[offset:min(len(f.tasks), offset+limit)], nil
}

func response(task state.AppTask) api.AppTaskResponse {
	return api.AppTaskResponse{ID: task.ID, Status: api.AppTaskStatus(task.Status), Command: task.Command, CreatedAt: task.CreatedAt.Format(time.RFC3339Nano)}
}

func build(store *fakeStore, query string, enabled bool) dashboard.AppTasksData {
	values, _ := url.ParseQuery(query)
	return apptaskdashboard.Build(context.Background(), store, apptaskdashboard.Request{AccountID: "a", AppID: "b", Slug: "orders", Enabled: enabled, Query: values, Response: response})
}

func tasks(n int) []state.AppTask {
	out := make([]state.AppTask, n)
	for i := range out {
		out[i] = state.AppTask{ID: "00000000-0000-4000-8000-" + strconv.FormatInt(int64(100000000000+i), 10), Status: "succeeded", Command: []string{"true"}, CreatedAt: time.Unix(0, 0)}
	}
	return out
}

func TestBuildPagesHistory(t *testing.T) {
	store := &fakeStore{tasks: tasks(apptaskdashboard.PageSize + 3)}
	first := build(store, "", true)
	if len(first.Tasks) != apptaskdashboard.PageSize || !first.HasNext || first.NextOffset != apptaskdashboard.PageSize || first.HasPrev {
		t.Fatalf("first page = %d tasks next=%v/%d prev=%v", len(first.Tasks), first.HasNext, first.NextOffset, first.HasPrev)
	}
	second := build(store, "offset=25", true)
	if len(second.Tasks) != 3 || second.HasNext || !second.HasPrev || second.PrevOffset != 0 {
		t.Fatalf("second page = %d tasks next=%v prev=%v/%d", len(second.Tasks), second.HasNext, second.HasPrev, second.PrevOffset)
	}
}

func TestBuildTellsAMissingTaskFromAnOutage(t *testing.T) {
	store := &fakeStore{tasks: tasks(1)}
	if data := build(store, "task="+taskID, true); data.Message != "That task was not found for this app." || data.Selected != nil {
		t.Fatalf("missing task: message=%q selected=%v", data.Message, data.Selected)
	}
	store.getErr = context.DeadlineExceeded
	if data := build(store, "task="+taskID, true); data.Message != "Task details are unavailable right now; try again shortly." {
		t.Fatalf("store timeout: message = %q, want it not to claim the task is missing", data.Message)
	}
	if data := build(&fakeStore{tasks: tasks(1)}, "task=not-a-uuid", true); data.Message != "That task ID is not valid." {
		t.Fatalf("bad id: message = %q", data.Message)
	}
	found := &fakeStore{tasks: []state.AppTask{{ID: taskID, Status: "failed", Command: []string{"sh", "-c", "echo a; rm b"}}}}
	if data := build(found, "task="+taskID, true); data.Selected == nil || data.Selected.Command != `sh -c 'echo a; rm b'` || len(found.offsets) != 0 {
		t.Fatalf("found task: selected=%+v listed=%v", data.Selected, found.offsets)
	}
}

func TestBuildReadsNothingWhileTheAPIIsOff(t *testing.T) {
	store := &fakeStore{tasks: tasks(2), listErr: errors.New("must not be called")}
	if data := build(store, "", false); data.Enabled || len(data.Tasks) != 0 || len(store.offsets) != 0 || data.Message != "" {
		t.Fatalf("disabled page = %+v, store calls %v", data, store.offsets)
	}
	if data := build(&fakeStore{listErr: errors.New("down")}, "", true); data.Message != "Task history is unavailable right now; try again shortly." {
		t.Fatalf("list outage message = %q", data.Message)
	}
}
