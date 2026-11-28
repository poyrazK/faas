package api

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Managed Redis-compatible caches on Valkey (ADR-20261116192126).
//
// A cache is a platform-managed internal app on compute nodes that runs the
// pinned Valkey image below. It is reached at
// redis://<name>.svc.gregale:6379 through the private TCP service proxy
// (ADR-576) and scales to zero like any app.

const (
	// ManagedValkeyCapability is the product capability (and platform flag)
	// that releases the /v1/redis surface.
	ManagedValkeyCapability = "managed-redis"

	// ManagedValkeyImage is the image apid deploys for a new cache, resize or
	// upgrade. It is digest-pinned (valkey/valkey:8.1.12-alpine); a new Valkey
	// release is a reviewed change here. Deploy references carry a digest and
	// no tag. A cache may run any image in managedValkeyImages: one becomes
	// ManagedValkeyImage only a release after it joined that list.
	ManagedValkeyImage = "docker.io/valkey/valkey@sha256:9971099af41c36e887aabdfe22b80c0fe45df949163dfc9cb434febc57cfc3fc"

	// ManagedValkeyPort is the port a cache listens on at its service address.
	ManagedValkeyPort = 6379

	// ManagedValkeyMaxMemoryMB caps a cache's Valkey maxmemory (owner,
	// 2026-11-16: "max 512mb (can change later)").
	ManagedValkeyMaxMemoryMB = 512
	// ManagedValkeyMinMemoryMB is the smallest cache.
	ManagedValkeyMinMemoryMB = 32
	// ManagedValkeyMemoryStepMB is the size granularity.
	ManagedValkeyMemoryStepMB = 32
	// ManagedValkeyDefaultMemoryMB is the size when none is requested.
	ManagedValkeyDefaultMemoryMB = 128
	// ManagedValkeyVMOverheadMB is added to maxmemory for the VM: guest
	// kernel, guest-init, Valkey's buffers and fragmentation headroom.
	ManagedValkeyVMOverheadMB = 64

	// ManagedValkeyDefaultIdleTimeoutS parks an idle cache after 15 minutes.
	ManagedValkeyDefaultIdleTimeoutS = 15 * 60
	// ManagedValkeyMinIdleTimeoutS and ManagedValkeyMaxIdleTimeoutS bound
	// the customer's idle timeout.
	ManagedValkeyMinIdleTimeoutS = 5 * 60
	ManagedValkeyMaxIdleTimeoutS = 24 * 60 * 60

	// ManagedValkeyDefaultBindingEnv is the env key a binding writes.
	ManagedValkeyDefaultBindingEnv = "REDIS_URL"
	// ManagedValkeyServiceDomain is the private service-address suffix.
	ManagedValkeyServiceDomain = "svc.gregale"
	// ManagedValkeyDefaultEvictionPolicy evicts least recently used keys.
	ManagedValkeyDefaultEvictionPolicy = "allkeys-lru"
)

// managedValkeyImages are the images a cache deployment may run: the one
// apid deploys and the others still running in existing caches. A cache
// keeps its image until its next handoff: `gregale redis update`, a resize
// and an eviction-policy change all deploy ManagedValkeyImage. Remove an
// image only once no cache deployment runs it (the query is in
// ADR-20261116192126).
var managedValkeyImages = []string{ManagedValkeyImage}

// ManagedValkeyImages returns a copy of the images a cache may run.
func ManagedValkeyImages() []string {
	return slices.Clone(managedValkeyImages)
}

// IsManagedValkeyImage reports whether ref is an image a cache may run.
func IsManagedValkeyImage(ref string) bool {
	return slices.Contains(managedValkeyImages, ref)
}

// IsManagedValkeyCommand reports whether entrypoint is one apid gives a cache
// deployment: ManagedValkeyServerCommand or ManagedValkeyHandoffCommand. A
// customer app that deploys the official Valkey image with its own command
// is not a managed cache.
func IsManagedValkeyCommand(entrypoint []string) bool {
	return len(entrypoint) == 3 && entrypoint[0] == "/bin/sh" && entrypoint[1] == "-c" &&
		strings.Contains(entrypoint[2], "valkey-server --port ") &&
		strings.Contains(entrypoint[2], " --maxmemory-policy ")
}

// ManagedValkeyEvictionPolicies are the maxmemory policies a cache may use.
var ManagedValkeyEvictionPolicies = []string{
	"allkeys-lru", "allkeys-lfu", "allkeys-random",
	"volatile-lru", "volatile-lfu", "volatile-random", "volatile-ttl",
	"noeviction",
}

// ManagedValkeyBlockedCommands are renamed away inside Valkey: nothing, the
// platform included, ever needs them on a cache. MIGRATE opens a connection
// from the cache VM to any host and port the client names, which made the
// cache a blind reachability probe of internal and metadata addresses (prod
// hunt r83, 2026-11-28).
var ManagedValkeyBlockedCommands = []string{
	"DEBUG", "MODULE", "SHUTDOWN", "SAVE", "BGSAVE", "BGREWRITEAOF",
	"FAILOVER", "CLUSTER", "ACL", "MONITOR", "MIGRATE",
}

// ManagedValkeyClientDeniedCommands are refused by the service proxy for
// every customer session. Valkey keeps them because the platform's resize
// and upgrade handoff uses them (replication, promotion, port change); the
// proxy is the only way into a cache, so it is where they are fenced.
var ManagedValkeyClientDeniedCommands = append([]string{
	"CONFIG", "REPLICAOF", "SLAVEOF", "SYNC", "PSYNC", "REPLCONF",
}, ManagedValkeyBlockedCommands...)

// ManagedValkeyHandoffWindowS is how long after a resize or upgrade is
// created its new VM boots as a replica of the running cache. A cold boot
// after the window starts empty, like any cache cold boot.
const ManagedValkeyHandoffWindowS = 15 * 60

// ManagedValkeyCachesFor is the number of caches a plan includes. Free has
// none (owner, 2026-11-16: Hobby and up).
func ManagedValkeyCachesFor(plan Plan) int {
	switch plan {
	case PlanHobby:
		return 1
	case PlanPro:
		return 3
	case PlanScale:
		return 10
	default:
		return 0
	}
}

// ManagedValkeyMaxMemoryFor is the largest cache a plan can hold: 512 MB, or
// less when the plan's largest instance cannot fit 512 MB plus the VM
// overhead. Zero means the plan includes no caches.
func ManagedValkeyMaxMemoryFor(plan Plan) int {
	if ManagedValkeyCachesFor(plan) == 0 {
		return 0
	}
	limits, ok := LimitsFor(plan)
	if !ok {
		return 0
	}
	room := limits.RAMMB - ManagedValkeyVMOverheadMB
	room -= room % ManagedValkeyMemoryStepMB
	return min(ManagedValkeyMaxMemoryMB, room)
}

// ManagedValkeyVMRAMMB is the instance RAM for a cache of memoryMB.
func ManagedValkeyVMRAMMB(memoryMB int) int {
	return memoryMB + ManagedValkeyVMOverheadMB
}

// ManagedValkeyHost is a cache's private service host name.
func ManagedValkeyHost(name string) string {
	return name + "." + ManagedValkeyServiceDomain
}

// ManagedValkeyURL is the connection URL a binding writes. It carries no
// password: the private service proxy authorizes every caller (owner,
// 2026-11-16: no Valkey password).
func ManagedValkeyURL(name string) string {
	return fmt.Sprintf("redis://%s:%d", ManagedValkeyHost(name), ManagedValkeyPort)
}

// ManagedValkeyAddr is the host:port form for clients configured that way.
func ManagedValkeyAddr(name string) string {
	return fmt.Sprintf("%s:%d", ManagedValkeyHost(name), ManagedValkeyPort)
}

// ValidateManagedValkeyMemory checks a requested size against the plan.
func ValidateManagedValkeyMemory(plan Plan, memoryMB int) *Problem {
	maxMB := ManagedValkeyMaxMemoryFor(plan)
	if maxMB == 0 {
		return ErrManagedValkeyNotInPlan(plan)
	}
	if memoryMB < ManagedValkeyMinMemoryMB || memoryMB > maxMB || memoryMB%ManagedValkeyMemoryStepMB != 0 {
		return ErrValidation(fmt.Sprintf("memory_mb must be a multiple of %d between %d and %d on the %s plan; got %d",
			ManagedValkeyMemoryStepMB, ManagedValkeyMinMemoryMB, maxMB, plan, memoryMB)).
			WithLimit(int64(maxMB), int64(memoryMB))
	}
	return nil
}

// ValidateManagedValkeyEvictionPolicy checks a maxmemory policy.
func ValidateManagedValkeyEvictionPolicy(policy string) *Problem {
	if !slices.Contains(ManagedValkeyEvictionPolicies, policy) {
		return ErrValidation(fmt.Sprintf("eviction_policy %q is not one of %v", policy, ManagedValkeyEvictionPolicies))
	}
	return nil
}

// ValidateManagedValkeyIdleTimeout checks an idle timeout; 0 means default.
func ValidateManagedValkeyIdleTimeout(seconds int) *Problem {
	if seconds == 0 {
		return nil
	}
	if seconds < ManagedValkeyMinIdleTimeoutS || seconds > ManagedValkeyMaxIdleTimeoutS {
		return ErrValidation(fmt.Sprintf("idle_timeout_s must be between %d and %d; got %d",
			ManagedValkeyMinIdleTimeoutS, ManagedValkeyMaxIdleTimeoutS, seconds))
	}
	return nil
}

// ManagedValkeyServerCommand is the argv a cache runs. There is no password:
// the cache has no public address, and the private service proxy admits only
// authorized callers in the cache's account.
func ManagedValkeyServerCommand(memoryMB int, policy string) []string {
	return []string{"/bin/sh", "-c", "exec " + managedValkeyServerArgs(memoryMB, policy, strconv.Itoa(ManagedValkeyPort))}
}

// managedValkeyServerArgs is the valkey-server command line, listening on
// port (0 = TCP closed).
func managedValkeyServerArgs(memoryMB int, policy, port string) string {
	args := []string{
		"valkey-server",
		"--port", port,
		"--bind", "0.0.0.0",
		"--protected-mode", "no",
		"--save", "''",
		"--appendonly", "no",
		"--maxmemory", strconv.Itoa(memoryMB) + "mb",
		"--maxmemory-policy", policy,
	}
	for _, command := range ManagedValkeyBlockedCommands {
		args = append(args, "--rename-command", command, "''")
	}
	return strings.Join(args, " ")
}

// ManagedValkeyHandoffCommand is the argv of a resize or upgrade
// (ADR-20261116192126, slice 4). Until handoffUntil (unix seconds) the new VM
// copies the running cache before it serves:
//
//  1. Valkey starts with TCP closed, on a unix socket, as a replica of the
//     cache's own service address. Its deployment has no traffic yet, so the
//     address reaches the running VM, waking it if it is parked.
//  2. After the full sync it pauses writes on the running VM (CLIENT PAUSE
//     WRITE, up to 300 s), waits until its offset matches, then promotes
//     itself and opens port 6379.
//  3. Readiness (TCP accept on 6379) passes, the ordinary rollout moves
//     traffic, and the old VM stops. Writes paused on it were never
//     acknowledged; their clients reconnect to the new VM.
//
// Any failure exits before port 6379 opens: the deployment never becomes
// ready, the rollout fails, and the running VM keeps serving (its pause is
// lifted, or expires). After the window the script starts an empty cache,
// as any cold boot does; inside the window a cold boot fails until it ends.
//
// source is the running cache's private address: apid passes its service IP
// so the handoff does not depend on guest DNS, which failed under load in
// native run redis-20261118T073206, and falls back to ManagedValkeyHost.
func ManagedValkeyHandoffCommand(source string, memoryMB int, policy string, handoffUntil int64) []string {
	port := strconv.Itoa(ManagedValkeyPort)
	script := strings.Join([]string{
		`if [ "$(date +%s)" -ge ` + strconv.FormatInt(handoffUntil, 10) + ` ]; then exec ` + managedValkeyServerArgs(memoryMB, policy, port) + `; fi`,
		`S=/tmp/valkey.sock`,
		managedValkeyServerArgs(memoryMB, policy, "0") + ` --unixsocket $S --unixsocketperm 700 --replicaof ` + source + ` ` + port + ` &`,
		`P=$!`,
		`l() { valkey-cli -s $S "$@"; }`,
		`r() { valkey-cli -h ` + source + ` -p ` + port + ` "$@"; }`,
		`f() { l INFO replication | tr -d '\r' | sed -n "s/^$1://p"; }`,
		`fail() { r CLIENT UNPAUSE >/dev/null 2>&1; kill $P; exit 1; }`,
		`i=0; until [ "$(f master_link_status)" = up ] && [ "$(f master_sync_in_progress)" = 0 ]; do i=$((i+1)); [ $i -gt 1200 ] && fail; sleep 0.5; done`,
		`r CLIENT PAUSE 300000 WRITE >/dev/null || fail`,
		`T=$(r INFO replication | tr -d '\r' | sed -n 's/^master_repl_offset://p')`,
		`[ -n "$T" ] || fail`,
		`i=0; until [ "$(f slave_repl_offset)" -ge "$T" ] 2>/dev/null; do i=$((i+1)); [ $i -gt 300 ] && fail; sleep 0.1; done`,
		`l REPLICAOF NO ONE >/dev/null || fail`,
		`l CONFIG SET port ` + port + ` >/dev/null || fail`,
		`wait $P`,
	}, "\n")
	return []string{"/bin/sh", "-c", script}
}

// IsManagedValkeyHandoffCommand reports whether entrypoint is a handoff
// deployment's command (ManagedValkeyHandoffCommand): it copies from the
// running cache instead of starting one.
func IsManagedValkeyHandoffCommand(entrypoint []string) bool {
	return len(entrypoint) == 3 && entrypoint[0] == "/bin/sh" && entrypoint[1] == "-c" &&
		strings.Contains(entrypoint[2], " --replicaof ")
}

// ErrManagedValkeyNotInPlan is the plan refusal for managed caches.
func ErrManagedValkeyNotInPlan(plan Plan) *Problem {
	return NewProblem(403, CodeManagedRedisNotInPlan, "Managed Redis is not included in this plan",
		fmt.Sprintf("The %s plan includes no managed Redis caches; upgrade to Hobby or above.", plan))
}

// ErrManagedValkeyLimit is the per-plan cache count refusal.
func ErrManagedValkeyLimit(plan Plan, limit int) *Problem {
	return NewProblem(403, CodeManagedRedisLimit, "Managed Redis cache limit reached",
		fmt.Sprintf("The %s plan includes %d managed Redis cache(s); delete one or upgrade.", plan, limit)).
		WithLimit(int64(limit), int64(limit+1))
}

// ComposeRedisConfiguredReason is a plan's not_provisioned_reason for a
// Compose redis service whose every user already has its key set (bound to a
// managed cache by an earlier apply, or set by hand). Feature hints read it to
// say the key is in place instead of asking for an external Redis.
const ComposeRedisConfiguredReason = "every service that uses it already has its key set"

// Problem codes for managed caches.
const (
	CodeManagedRedisNotInPlan = "managed_redis_not_in_plan"
	CodeManagedRedisLimit     = "managed_redis_limit"
)

// RedisCache is one managed cache on the wire.
type RedisCache struct {
	Name           string    `json:"name"`
	AppID          string    `json:"app_id"`
	MemoryMB       int       `json:"memory_mb"`
	EvictionPolicy string    `json:"eviction_policy"`
	IdleTimeoutS   int       `json:"idle_timeout_s"`
	Engine         string    `json:"engine"`
	Host           string    `json:"host"`
	Port           int       `json:"port"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
}

// CreateRedisCacheRequest is POST /v1/redis.
type CreateRedisCacheRequest struct {
	Name           string `json:"name"`
	MemoryMB       int    `json:"memory_mb,omitempty"`
	EvictionPolicy string `json:"eviction_policy,omitempty"`
	IdleTimeoutS   int    `json:"idle_timeout_s,omitempty"`
}

// ListRedisCachesResponse is GET /v1/redis.
type ListRedisCachesResponse struct {
	Caches []RedisCache `json:"caches"`
	Limit  int          `json:"limit"`
}

// UpdateRedisCacheRequest is PATCH /v1/redis/{name}: a resize or eviction
// policy change. The cache's data is copied to the new VM before it serves.
type UpdateRedisCacheRequest struct {
	MemoryMB       int    `json:"memory_mb,omitempty"`
	EvictionPolicy string `json:"eviction_policy,omitempty"`
	// Evict allows shrinking below the data the cache holds; keys are then
	// evicted by its policy during the copy.
	Evict bool `json:"evict,omitempty"`
}

// RedisCacheChange answers a resize, policy change or upgrade.
type RedisCacheChange struct {
	Cache        RedisCache `json:"cache"`
	DeploymentID string     `json:"deployment_id"`
}

// BindRedisCacheRequest is POST /v1/redis/{name}/bindings.
type BindRedisCacheRequest struct {
	App    string `json:"app"`
	EnvKey string `json:"env_key,omitempty"`
}

// RedisCacheBinding is the result of a binding. Binding also grants the app
// the call to the cache (ADR-20261124174500).
type RedisCacheBinding struct {
	Cache  string `json:"cache"`
	App    string `json:"app"`
	EnvKey string `json:"env_key"`
}

// RedisCacheUnbinding answers DELETE /v1/redis/{name}/bindings/{app}: the
// app may no longer call the cache. Its connection variable stays set.
type RedisCacheUnbinding struct {
	Cache string `json:"cache"`
	App   string `json:"app"`
}
