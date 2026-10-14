package api

import (
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
	"reflect"
	"testing"
)

// adr: 1036
func TestObjectStorageComputeTriggerValidation(t *testing.T) {
	in := []ObjectStorageComputeTrigger{{ID: "images", Prefix: "uploads/", Suffix: ".png"}}
	normal, err := NormalizeObjectStorageComputeTriggers(in)
	if err != nil || normal[0].Events[0] != "s3:ObjectCreated:*" || len(in[0].Events) != 0 {
		t.Fatal(normal, err)
	}
	for _, bad := range [][]ObjectStorageComputeTrigger{
		{{ID: ""}}, {{ID: "bad\nid"}}, {{ID: "x", Events: []string{"s3:ObjectRemoved:Delete"}}}, {{ID: "x", Events: []string{"s3:ObjectCreated:Unknown"}}},
		{{ID: "x"}, {ID: "x", Prefix: "different/"}}, {{ID: "a", Prefix: "images/"}, {ID: "b", Prefix: "images/large/"}},
	} {
		if _, err := NormalizeObjectStorageComputeTriggers(bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	account, app, bucket := uuid.NewString(), uuid.NewString(), uuid.NewString()
	rules, err := ObjectStorageComputeTriggerRules(bucket, "us-east-1", account, app, in)
	again, e := ObjectStorageComputeTriggerRules(bucket, "us-east-1", account, app, in)
	if err != nil || e != nil || !reflect.DeepEqual(rules, again) || rules[0].Destination != "arn:gregale:lambda:us-east-1:"+account+":function:"+app {
		t.Fatal(rules, err, e)
	}
	other, _ := ObjectStorageComputeTriggerRules(uuid.NewString(), "us-east-1", account, app, in)
	if other[0].ID == rules[0].ID {
		t.Fatal("identity not bucket-scoped")
	}
}

func TestDevBucketComputeTriggerIntent(t *testing.T) {
	for _, tc := range []struct {
		yaml      string
		specified bool
		count     int
	}{
		{"name: assets\n", false, 0}, {"name: assets\ntriggers: []\n", true, 0},
		{"name: assets\ntriggers:\n  - id: images\n    prefix: uploads/\n", true, 1},
	} {
		var bucket DevBucketRequest
		if err := yaml.Unmarshal([]byte(tc.yaml), &bucket); err != nil {
			t.Fatal(err)
		}
		normal, err := NormalizeDevBuckets([]DevBucketRequest{bucket})
		if err != nil || (normal[0].Triggers != nil) != tc.specified {
			t.Fatal(normal, err)
		}
		if tc.specified && len(*normal[0].Triggers) != tc.count {
			t.Fatal(normal)
		}
		if tc.count > 0 {
			(*normal[0].Triggers)[0].Events[0] = "evil"
			if len((*bucket.Triggers)[0].Events) != 0 {
				t.Fatal("normalization aliases input")
			}
		}
	}
}
