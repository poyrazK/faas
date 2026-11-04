// adr: 20261103160000

package state

import (
	"reflect"
	"testing"
)

func TestCloneBucketBlockersNameEachUncopyableBucketOnce(t *testing.T) {
	bucket := func(id, name, backend string, versioned bool) ProjectEnvironmentCloneObjectBucket {
		return ProjectEnvironmentCloneObjectBucket{ID: id, Name: name, BackendID: backend, VersioningEnabled: versioned}
	}
	record := func(buckets ...ProjectEnvironmentCloneObjectBucket) projectCloneWorkloadRecord {
		return projectCloneWorkloadRecord{snapshot: projectCloneWorkloadSnapshot{Bindings: &ProjectEnvironmentCloneBindingDefinitions{Buckets: buckets}}}
	}
	shared := bucket("3", "shared", "gcs", false)
	records := []projectCloneWorkloadRecord{
		record(bucket("1", "uploads", "ovh", true), bucket("2", "logs", "ovh", false), shared),
		record(shared),
		{},
	}
	got := cloneBucketBlockers(records, []string{"ovh"})
	want := []ProjectEnvironmentCloneCoverageBlocker{
		{Table: "object_buckets", Resource: "logs", Code: CloneBlockerObjectVersioningRequired},
		{Table: "object_buckets", Resource: "shared", Code: CloneBlockerObjectStorageProviderUnqualified},
		{Table: "object_buckets", Resource: "shared", Code: CloneBlockerObjectVersioningRequired},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("blockers = %+v, want %+v", got, want)
	}
	if ids := cloneSourceBucketIDs(records); !reflect.DeepEqual(ids, []string{"1", "2", "3"}) {
		t.Fatalf("bucket ids = %v", ids)
	}
	if got := cloneBucketBlockers(records[:1], []string{"ovh", "gcs"}); len(got) != 2 {
		t.Fatalf("qualified backends still blocked: %+v", got)
	}
	if got := cloneBucketBlockers(nil, nil); len(got) != 0 {
		t.Fatalf("no buckets blocked: %+v", got)
	}
}
