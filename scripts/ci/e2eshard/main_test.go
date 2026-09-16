package main

import (
	"reflect"
	"testing"
)

func TestAssignedShardPartitionsEveryTestExactlyOnce(t *testing.T) {
	tests := []string{"TestApplyProject_Diff_Changed", "TestScanProject_MultiTier", "TestWebhookE2E_Retry"}
	const shards = 4
	seen := make(map[string]int)
	for shard := 0; shard < shards; shard++ {
		for _, name := range tests {
			if assignedShard(name, shards) == shard {
				seen[name]++
			}
		}
	}
	for _, name := range tests {
		if seen[name] != 1 {
			t.Fatalf("%s assigned %d times", name, seen[name])
		}
	}
}

func TestPartitionBootTests(t *testing.T) {
	regular, boot := partitionBootTests([]string{
		"TestApplyProject_Diff_Changed",
		"TestBootContract_APIDRenderedConfigAndProductionListeners",
	})
	if !reflect.DeepEqual(regular, []string{"TestApplyProject_Diff_Changed"}) ||
		!reflect.DeepEqual(boot, []string{"TestBootContract_APIDRenderedConfigAndProductionListeners"}) {
		t.Fatalf("regular=%v boot=%v", regular, boot)
	}
}

func TestIsGoTestName(t *testing.T) {
	for _, name := range []string{"TestApplyProject", "Test_Edge", "Test1"} {
		if !isGoTestName(name) {
			t.Errorf("isGoTestName(%q)=false", name)
		}
	}
	for _, name := range []string{"Test", "Tester", "BenchmarkApply", "helper"} {
		if isGoTestName(name) {
			t.Errorf("isGoTestName(%q)=true", name)
		}
	}
}
