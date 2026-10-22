package api

import (
	"context"
	"io"
	"net/url"
	"strconv"
	"strings"
)

// Live job task output (ADR-1163). The stream route renders the app log
// stream's SSE frames (`log`, `gap`, heartbeat comments) and ends with one
// `end` frame whose reason is one of the JobTaskLogsEnd* values.
const (
	// JobTaskLogsEndFinished: the task is terminal and its last VM's output
	// was relayed. Fetch the task row for the outcome.
	JobTaskLogsEndFinished = "task_finished"
	// JobTaskLogsEndTimeout: the stream's backstop elapsed.
	JobTaskLogsEndTimeout = "timeout"
	// JobTaskLogsEndUnreachable: the node running the task could not be
	// reached; the stored output is still available once the task ends.
	JobTaskLogsEndUnreachable = "node_unreachable"
	// JobTaskLogsEndNotFound: the job, run or task disappeared.
	JobTaskLogsEndNotFound = "not_found"
	// JobTaskLogsEndStoredOutput: the task finished before its last VM's
	// output could be streamed; read the stored output from GET .../logs.
	JobTaskLogsEndStoredOutput = "stored_output"
)

// IsJobTaskLogStreamPath reports whether p is
// /v1/jobs/{name}/runs/{id}/tasks/{idx}/logs/stream.
func IsJobTaskLogStreamPath(p string) bool {
	const prefix = "/v1/jobs/"
	if !strings.HasPrefix(p, prefix) {
		return false
	}
	parts := strings.Split(p[len(prefix):], "/")
	return len(parts) == 7 && parts[0] != "" && parts[1] == "runs" && parts[2] != "" &&
		parts[3] == "tasks" && parts[4] != "" && parts[5] == "logs" && parts[6] == "stream"
}

// StreamJobTaskLogs opens the live output of one job task. sinceSeq resumes
// within the current attempt; 0 replays the attempt from its first line.
func (c *Client) StreamJobTaskLogs(ctx context.Context, job, runID string, taskIndex int, sinceSeq int64) (io.ReadCloser, error) {
	path := "/v1/jobs/" + url.PathEscape(job) + "/runs/" + url.PathEscape(runID) +
		"/tasks/" + strconv.Itoa(taskIndex) + "/logs/stream"
	if sinceSeq > 0 {
		path += "?since_seq=" + strconv.FormatInt(sinceSeq, 10)
	}
	return c.stream(ctx, path)
}
