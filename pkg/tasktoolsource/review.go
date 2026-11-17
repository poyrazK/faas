// Package tasktoolsource validates non-executable hosted source reviews.
package tasktoolsource

import (
	"encoding/json"
	"errors"
	"reflect"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"

	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

var ErrInvalid = errors.New("invalid hosted Task source review")

type Review = api.HostedTaskSourceReview

func Fingerprint(r Review) string {
	r.Fingerprint = ""
	body, _ := json.Marshal(r)
	return toolpublication.Digest(append([]byte("hosted-task-source-review:v1:"), body...))
}
func Intent(r Review) api.TaskToolReleaseIntent {
	return api.TaskToolReleaseIntent{ReleaseFingerprint: r.Fingerprint, WorkerRuntime: r.WorkerRuntime, Protocol: r.Protocol, ObserverMetricApp: r.ObserverMetricApp, Endpoint: r.Endpoint, AccessMode: r.AccessMode, ToolScopes: r.ToolScopes, Apps: r.Apps, RequiredSetup: r.RequiredSetup, Reason: "Review uploaded Task release"}
}
func Decode(raw json.RawMessage) (Review, error) {
	var r Review
	// The embedded review is a closed metadata DTO. Canonical round-trip also
	// rejects unknown properties and values dropped by DTO decoding.
	if len(raw) > api.TaskToolReleaseIntentMaxBytes || json.Unmarshal(raw, &r) != nil {
		return r, ErrInvalid
	}
	canonical, _ := json.Marshal(r)
	var original, projected any
	if json.Unmarshal(raw, &original) != nil || json.Unmarshal(canonical, &projected) != nil || !reflect.DeepEqual(original, projected) {
		return r, ErrInvalid
	}
	if r.Kind != "hosted_task_source_review" || uuid.Validate(r.AccountID) != nil || r.Version != 1 || r.Status != "review_only" || r.ReadyToApply || r.BindingsVerified || !r.MigrationsRequired || r.TimeoutSeconds < 1 || r.TimeoutSeconds > 600 || len(r.ReadinessRoles) != 0 || len(r.Sources) != 2 || !toolpublication.ValidID(r.Sources["web"]) || !toolpublication.ValidID(r.Sources["worker"]) || Fingerprint(r) != r.Fingerprint || tasktoolrelease.Validate(Intent(r)) != nil {
		return r, ErrInvalid
	}
	return r, nil
}
