package runcontrols

import (
	"github.com/onebox-faas/faas/pkg/api"
	"net/url"
	"strconv"
	"time"
)

func HistoryFilter(q url.Values) (api.RunHistoryFilter, error) {
	f := api.RunHistoryFilter{}
	for k, values := range q {
		if len(values) != 1 {
			return f, api.ErrValidation("history filters must be singular")
		}
		switch k {
		case "task_name", "task_version", "customer_ref", "agent_ref", "status", "since", "until", "cursor":
		default:
			return f, api.ErrValidation("unknown history filter")
		}
	}
	f.TaskName = q.Get("task_name")
	f.CustomerRef = q.Get("customer_ref")
	f.AgentRef = q.Get("agent_ref")
	f.Status = api.ExecutionStatus(q.Get("status"))
	f.Cursor = q.Get("cursor")
	var err error
	if s := q.Get("task_version"); s != "" {
		f.TaskVersion, err = strconv.Atoi(s)
		if err != nil {
			return f, api.ErrValidation("invalid task version")
		}
	}
	if s := q.Get("since"); s != "" {
		f.Since, err = time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return f, api.ErrValidation("since must be RFC 3339")
		}
	}
	if s := q.Get("until"); s != "" {
		f.Until, err = time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return f, api.ErrValidation("until must be RFC 3339")
		}
	}
	return f, nil
}
