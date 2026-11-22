package capgrant

import "testing"

func TestNormalizedDefaultsAndValidate(t *testing.T) {
	u := Uses{
		Queues:       []QueueGrant{{App: " Worker "}},
		Realtime:     []RealtimeGrant{{App: "self", Channels: "room:*"}},
		DelayedTasks: []string{"SELF"},
		Buckets:      []BucketGrant{{Bucket: "assets", Prefix: "rooms/"}},
	}.Normalized()
	if u.Queues[0].App != "worker" || u.Queues[0].Queue != Any {
		t.Fatalf("queue defaults: %+v", u.Queues[0])
	}
	if u.DelayedTasks[0] != Self {
		t.Fatalf("delayed tasks app: %q", u.DelayedTasks[0])
	}
	if b := u.Buckets[0]; b.App != Self || b.Access != AccessReadWrite {
		t.Fatalf("bucket defaults: %+v", b)
	}
	if err := u.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := (Uses{Buckets: []BucketGrant{{Bucket: "b", Prefix: "rooms"}}}).Normalized().Buckets[0].Prefix; got != "rooms/" {
		t.Fatalf("prefix without slash = %q, want rooms/", got)
	}
}

func TestValidateRefuses(t *testing.T) {
	for name, u := range map[string]Uses{
		"missing app":      {Queues: []QueueGrant{{Queue: "q"}}},
		"inner star":       {Queues: []QueueGrant{{App: "w", Queue: "a*b"}}},
		"double star":      {Realtime: []RealtimeGrant{{App: "w", Channels: "a**"}}},
		"app slash":        {DelayedTasks: []string{"a/b"}},
		"bucket access":    {Buckets: []BucketGrant{{Bucket: "b", Access: "admin"}}},
		"absolute prefix":  {Buckets: []BucketGrant{{Bucket: "b", Prefix: "/x"}}},
		"traversal prefix": {Buckets: []BucketGrant{{Bucket: "b", Prefix: "a/../b"}}},
		"missing bucket":   {Buckets: []BucketGrant{{App: "self"}}},
	} {
		if err := u.Normalized().Validate(); err == nil {
			t.Errorf("%s: validated", name)
		}
	}
	many := Uses{}
	for range MaxGrantsPerKind + 1 {
		many.DelayedTasks = append(many.DelayedTasks, "self")
	}
	if err := many.Validate(); err == nil {
		t.Error("over the per-kind cap validated")
	}
}

func TestPermits(t *testing.T) {
	u := Uses{
		Queues:       []QueueGrant{{App: "worker", Queue: "transforms"}, {App: "self"}},
		Realtime:     []RealtimeGrant{{App: "assetforge-web", Channels: "room:*"}},
		DelayedTasks: []string{"self"},
	}
	worker := Target{Slug: "assetforge-worker", WorkloadName: "worker", SameProject: true}
	self := Target{Self: true, Slug: "assetforge-mcp", WorkloadName: "mcp", SameProject: true}
	web := Target{Slug: "assetforge-web", WorkloadName: "web", SameProject: true}
	otherProjectWorker := Target{Slug: "other-worker", WorkloadName: "worker"}

	if !u.PermitsQueue(worker, "transforms") || u.PermitsQueue(worker, "other") || u.PermitsQueue(worker, "") {
		t.Error("worker queue grant")
	}
	if u.PermitsQueue(otherProjectWorker, "transforms") {
		t.Error("service name matched an app outside the project")
	}
	if !u.PermitsQueue(self, "") || !u.PermitsQueue(self, "anything") {
		t.Error("self * grant")
	}
	if !u.PermitsRealtime(web, "room:1") || u.PermitsRealtime(web, "room") || u.PermitsRealtime(web, "lobby") {
		t.Error("realtime prefix grant")
	}
	if !u.PermitsDelayedTasks(self) || u.PermitsDelayedTasks(worker) {
		t.Error("delayed tasks self grant")
	}
	if !u.PermitsAny(KindQueues, worker) || u.PermitsAny(KindRealtime, worker) {
		t.Error("PermitsAny")
	}
	if (Uses{}).PermitsQueue(self, "") {
		t.Error("empty uses permitted")
	}
}

func TestCloneIsDeep(t *testing.T) {
	var nilUses *Uses
	if nilUses.Clone() != nil {
		t.Fatal("nil clone")
	}
	u := &Uses{DelayedTasks: []string{"self"}}
	c := u.Clone()
	c.DelayedTasks[0] = "x"
	if u.DelayedTasks[0] != "self" {
		t.Fatal("clone shares backing array")
	}
}
