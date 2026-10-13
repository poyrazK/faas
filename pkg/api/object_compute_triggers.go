package api

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// ObjectStorageComputeTrigger targets only the app owning the bucket.
type ObjectStorageComputeTrigger struct {
	ID     string   `json:"id" yaml:"id"`
	Events []string `json:"events,omitempty" yaml:"events,omitempty"`
	Prefix string   `json:"prefix,omitempty" yaml:"prefix,omitempty"`
	Suffix string   `json:"suffix,omitempty" yaml:"suffix,omitempty"`
}

type ReconcileObjectStorageComputeTriggersRequest struct {
	ExpectedRevision *int64                        `json:"expected_revision"`
	Triggers         []ObjectStorageComputeTrigger `json:"triggers"`
}

// NormalizeObjectStorageComputeTriggers shares notification bounds and overlap
// validation. Names are mandatory: retries must never generate a new identity.
func NormalizeObjectStorageComputeTriggers(in []ObjectStorageComputeTrigger) ([]ObjectStorageComputeTrigger, error) {
	out := make([]ObjectStorageComputeTrigger, len(in))
	rules := make([]ObjectNotificationRule, len(in))
	for i, trigger := range in {
		if !validObjectNotificationID(trigger.ID) {
			return nil, fmt.Errorf("compute trigger requires a valid stable id")
		}
		trigger.Events = append([]string(nil), trigger.Events...)
		if len(trigger.Events) == 0 {
			trigger.Events = []string{"s3:ObjectCreated:*"}
		}
		for _, event := range trigger.Events {
			if !strings.HasPrefix(event, "s3:ObjectCreated:") {
				return nil, fmt.Errorf("compute triggers support ObjectCreated events only")
			}
		}
		rules[i] = ObjectNotificationRule{ID: trigger.ID, Events: trigger.Events, Prefix: trigger.Prefix, Suffix: trigger.Suffix,
			Destination: "arn:gregale:lambda:validation:00000000-0000-4000-8000-000000000000:function:00000000-0000-4000-8000-000000000001"}
		out[i] = trigger
	}
	normal, err := NormalizeObjectNotificationRules(rules)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Events = normal[i].Events
	}
	return out, nil
}

func ObjectStorageComputeTriggerRules(bucket, region, account, app string, in []ObjectStorageComputeTrigger) ([]ObjectNotificationRule, error) {
	triggers, err := NormalizeObjectStorageComputeTriggers(in)
	if err != nil {
		return nil, err
	}
	accountID, err := uuid.Parse(account)
	if err != nil {
		return nil, err
	}
	appID, err := uuid.Parse(app)
	if err != nil {
		return nil, err
	}
	out := make([]ObjectNotificationRule, len(triggers))
	for i, trigger := range triggers {
		id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale/compute-trigger/"+bucket+"/"+trigger.ID)).String()
		out[i] = ObjectNotificationRule{ID: "gregale-compute-" + id, Events: trigger.Events, Prefix: trigger.Prefix, Suffix: trigger.Suffix,
			Destination: "arn:gregale:lambda:" + region + ":" + accountID.String() + ":function:" + appID.String()}
	}
	return NormalizeObjectNotificationRules(out)
}
