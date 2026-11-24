// Package capgrant is the `uses:` block of a project service: the actions a
// workload may take on itself or a sibling app with a workload-identity token
// instead of an API key (ADR-20261122191423). It has no dependencies beyond
// the standard library so the project config parser, the API types and the
// state layer share one definition.
package capgrant

import (
	"fmt"
	"reflect"
	"strings"
)

// Self names the calling app in a grant's app field.
const Self = "self"

// Any matches every queue or channel.
const Any = "*"

// Bucket access values.
const (
	AccessRead      = "read"
	AccessWrite     = "write"
	AccessReadWrite = "read_write"
)

// MaxGrantsPerKind bounds each list so a manifest stays small.
const MaxGrantsPerKind = 64

const maxNameLen = 128

// Uses is one service's declared grants.
type Uses struct {
	Queues       []QueueGrant    `yaml:"queues,omitempty" json:"queues,omitempty"`
	Realtime     []RealtimeGrant `yaml:"realtime,omitempty" json:"realtime,omitempty"`
	DelayedTasks []string        `yaml:"delayed_tasks,omitempty" json:"delayed_tasks,omitempty"`
	Buckets      []BucketGrant   `yaml:"buckets,omitempty" json:"buckets,omitempty"`
}

// QueueGrant allows sending to a queue of App.
type QueueGrant struct {
	App   string `yaml:"app" json:"app"`
	Queue string `yaml:"queue,omitempty" json:"queue,omitempty"`
}

// RealtimeGrant allows publishing to channels of App's realtime endpoints.
type RealtimeGrant struct {
	App      string `yaml:"app" json:"app"`
	Channels string `yaml:"channels,omitempty" json:"channels,omitempty"`
}

// BucketGrant allows object access to a bucket owned by App.
type BucketGrant struct {
	Bucket string `yaml:"bucket" json:"bucket"`
	App    string `yaml:"app,omitempty" json:"app,omitempty"`
	Prefix string `yaml:"prefix,omitempty" json:"prefix,omitempty"`
	Access string `yaml:"access,omitempty" json:"access,omitempty"`
}

// IsZero reports whether no grant is declared.
func (u Uses) IsZero() bool {
	return len(u.Queues) == 0 && len(u.Realtime) == 0 && len(u.DelayedTasks) == 0 && len(u.Buckets) == 0
}

// Clone returns a deep copy; nil stays nil.
func (u *Uses) Clone() *Uses {
	if u == nil {
		return nil
	}
	out := Uses{
		Queues:       append([]QueueGrant(nil), u.Queues...),
		Realtime:     append([]RealtimeGrant(nil), u.Realtime...),
		DelayedTasks: append([]string(nil), u.DelayedTasks...),
		Buckets:      append([]BucketGrant(nil), u.Buckets...),
	}
	return &out
}

// Equal reports whether a and b grant the same actions; nil equals an empty
// block.
func Equal(a, b *Uses) bool {
	var left, right Uses
	if a != nil {
		left = a.Normalized()
	}
	if b != nil {
		right = b.Normalized()
	}
	if left.IsZero() || right.IsZero() {
		return left.IsZero() == right.IsZero()
	}
	return reflect.DeepEqual(left, right)
}

// Normalized trims values, lowercases app names and fills defaults: queue and
// channels default to "*", a bucket's app to self and its access to
// read_write. It does not validate.
func (u Uses) Normalized() Uses {
	out := Uses{}
	for _, g := range u.Queues {
		g.App = normalizeApp(g.App)
		g.Queue = strings.TrimSpace(g.Queue)
		if g.Queue == "" {
			g.Queue = Any
		}
		out.Queues = append(out.Queues, g)
	}
	for _, g := range u.Realtime {
		g.App = normalizeApp(g.App)
		g.Channels = strings.TrimSpace(g.Channels)
		if g.Channels == "" {
			g.Channels = Any
		}
		out.Realtime = append(out.Realtime, g)
	}
	for _, app := range u.DelayedTasks {
		out.DelayedTasks = append(out.DelayedTasks, normalizeApp(app))
	}
	for _, g := range u.Buckets {
		g.Bucket = strings.TrimSpace(g.Bucket)
		g.App = normalizeApp(g.App)
		if g.App == "" {
			g.App = Self
		}
		g.Prefix = strings.TrimSpace(g.Prefix)
		// The gateway matches a raw key prefix, so "rooms" would also allow
		// "rooms-archive/": a prefix always names a folder.
		if g.Prefix != "" && !strings.HasSuffix(g.Prefix, "/") {
			g.Prefix += "/"
		}
		g.Access = strings.ToLower(strings.TrimSpace(g.Access))
		if g.Access == "" {
			g.Access = AccessReadWrite
		}
		out.Buckets = append(out.Buckets, g)
	}
	return out
}

func normalizeApp(app string) string { return strings.ToLower(strings.TrimSpace(app)) }

// Validate checks a normalized block.
func (u Uses) Validate() error {
	if len(u.Queues) > MaxGrantsPerKind || len(u.Realtime) > MaxGrantsPerKind || len(u.DelayedTasks) > MaxGrantsPerKind || len(u.Buckets) > MaxGrantsPerKind {
		return fmt.Errorf("uses allows at most %d entries per kind", MaxGrantsPerKind)
	}
	for i, g := range u.Queues {
		if err := validApp(g.App); err != nil {
			return fmt.Errorf("uses.queues[%d]: %w", i, err)
		}
		if err := validPattern(g.Queue); err != nil {
			return fmt.Errorf("uses.queues[%d].queue: %w", i, err)
		}
	}
	for i, g := range u.Realtime {
		if err := validApp(g.App); err != nil {
			return fmt.Errorf("uses.realtime[%d]: %w", i, err)
		}
		if err := validPattern(g.Channels); err != nil {
			return fmt.Errorf("uses.realtime[%d].channels: %w", i, err)
		}
	}
	for i, app := range u.DelayedTasks {
		if err := validApp(app); err != nil {
			return fmt.Errorf("uses.delayed_tasks[%d]: %w", i, err)
		}
	}
	for i, g := range u.Buckets {
		if err := validName(g.Bucket); err != nil {
			return fmt.Errorf("uses.buckets[%d].bucket: %w", i, err)
		}
		if err := validApp(g.App); err != nil {
			return fmt.Errorf("uses.buckets[%d]: %w", i, err)
		}
		if strings.HasPrefix(g.Prefix, "/") || strings.Contains(g.Prefix, "..") || len(g.Prefix) > 512 {
			return fmt.Errorf("uses.buckets[%d].prefix must be a relative key prefix without \"..\", got %q", i, g.Prefix)
		}
		switch g.Access {
		case AccessRead, AccessWrite, AccessReadWrite:
		default:
			return fmt.Errorf("uses.buckets[%d].access must be read, write or read_write, got %q", i, g.Access)
		}
	}
	return nil
}

func validApp(app string) error {
	if app == "" {
		return fmt.Errorf("app is required (a project service name, an app slug, or self)")
	}
	return validName(app)
}

func validName(name string) error {
	if name == "" {
		return fmt.Errorf("must not be empty")
	}
	if len(name) > maxNameLen {
		return fmt.Errorf("must be at most %d characters", maxNameLen)
	}
	for _, r := range name {
		if r <= ' ' || r == 0x7f || r == '*' || r == '/' {
			return fmt.Errorf("%q must not contain spaces, control characters, '/' or '*'", name)
		}
	}
	return nil
}

// validPattern admits "*", a literal name, or a name with one trailing "*".
func validPattern(pattern string) error {
	if pattern == Any {
		return nil
	}
	literal := strings.TrimSuffix(pattern, "*")
	if literal == "" || len(pattern) > maxNameLen || strings.Contains(literal, "*") {
		return fmt.Errorf("%q must be *, a name, or a name prefix ending in one *", pattern)
	}
	for _, r := range literal {
		if r <= ' ' || r == 0x7f {
			return fmt.Errorf("%q must not contain spaces or control characters", pattern)
		}
	}
	return nil
}

func matchPattern(pattern, value string) bool {
	if pattern == Any {
		return true
	}
	if prefix, ok := strings.CutSuffix(pattern, "*"); ok {
		return value != "" && strings.HasPrefix(value, prefix)
	}
	return value == pattern
}

// Target is the app a call acts on, as seen from the caller.
type Target struct {
	// Self is true when the target is the calling app.
	Self bool
	// Slug is the target app's current slug.
	Slug string
	// WorkloadName is the target's service name; it matches only when the
	// target is in the caller's project.
	WorkloadName string
	SameProject  bool
}

// Matches reports whether a grant's app field names t.
func (t Target) Matches(app string) bool {
	app = normalizeApp(app)
	if app == Self {
		return t.Self
	}
	if app == "" {
		return false
	}
	if strings.EqualFold(app, t.Slug) {
		return true
	}
	return t.SameProject && t.WorkloadName != "" && strings.EqualFold(app, t.WorkloadName)
}

// PermitsQueue reports whether u allows sending to queue on t. An empty queue
// name (the app's default queue) matches only a "*" grant.
func (u Uses) PermitsQueue(t Target, queue string) bool {
	for _, g := range u.Normalized().Queues {
		if t.Matches(g.App) && matchPattern(g.Queue, queue) {
			return true
		}
	}
	return false
}

// PermitsRealtime reports whether u allows publishing to channel on t.
func (u Uses) PermitsRealtime(t Target, channel string) bool {
	for _, g := range u.Normalized().Realtime {
		if t.Matches(g.App) && matchPattern(g.Channels, channel) {
			return true
		}
	}
	return false
}

// PermitsDelayedTasks reports whether u allows scheduling delayed tasks on t.
func (u Uses) PermitsDelayedTasks(t Target) bool {
	for _, app := range u.Normalized().DelayedTasks {
		if t.Matches(app) {
			return true
		}
	}
	return false
}

// PermitsAny reports whether u holds any grant for kind on t, so a refusal
// can say whether the target or only the resource is missing.
func (u Uses) PermitsAny(kind Kind, t Target) bool {
	n := u.Normalized()
	switch kind {
	case KindQueues:
		for _, g := range n.Queues {
			if t.Matches(g.App) {
				return true
			}
		}
	case KindRealtime:
		for _, g := range n.Realtime {
			if t.Matches(g.App) {
				return true
			}
		}
	case KindDelayedTasks:
		return u.PermitsDelayedTasks(t)
	}
	return false
}

// Kind names a grant list.
type Kind string

const (
	KindQueues       Kind = "queues"
	KindRealtime     Kind = "realtime"
	KindDelayedTasks Kind = "delayed_tasks"
	KindBuckets      Kind = "buckets"
)
