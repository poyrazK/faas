package api

import (
	"fmt"
	"slices"
	"strconv"
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

	// ManagedValkeyImage is the only image a managed cache app runs. It is
	// digest-pinned (valkey/valkey:8.1.4-alpine); a new Valkey release is a
	// reviewed change here. Deploy references carry a digest and no tag.
	ManagedValkeyImage = "docker.io/valkey/valkey@sha256:e706d1213aaba6896c162bb6a3a9e1894e1a435f28f8f856d14fab2e10aa098b"

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

	// ManagedValkeyPasswordEnv is the app secret that holds the cache's
	// Valkey password inside its own VM.
	ManagedValkeyPasswordEnv = "VALKEY_PASSWORD"
	// ManagedValkeyDefaultBindingEnv is the env key a binding writes.
	ManagedValkeyDefaultBindingEnv = "REDIS_URL"
	// ManagedValkeyServiceDomain is the private service-address suffix.
	ManagedValkeyServiceDomain = "svc.gregale"
	// ManagedValkeyDefaultEvictionPolicy evicts least recently used keys.
	ManagedValkeyDefaultEvictionPolicy = "allkeys-lru"
)

// ManagedValkeyEvictionPolicies are the maxmemory policies a cache may use.
var ManagedValkeyEvictionPolicies = []string{
	"allkeys-lru", "allkeys-lfu", "allkeys-random",
	"volatile-lru", "volatile-lfu", "volatile-random", "volatile-ttl",
	"noeviction",
}

// ManagedValkeyBlockedCommands are renamed away so a client cannot reach
// them: they change the server's configuration, persistence, replication or
// lifecycle, which the platform owns.
var ManagedValkeyBlockedCommands = []string{
	"CONFIG", "DEBUG", "MODULE", "SHUTDOWN", "SAVE", "BGSAVE", "BGREWRITEAOF",
	"REPLICAOF", "SLAVEOF", "FAILOVER", "CLUSTER", "ACL", "MONITOR", "SYNC", "PSYNC",
}

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

// ManagedValkeyURL is the connection URL a binding writes.
func ManagedValkeyURL(name, password string) string {
	return fmt.Sprintf("redis://default:%s@%s:%d", password, ManagedValkeyHost(name), ManagedValkeyPort)
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

// ManagedValkeyServerCommand is the argv a cache runs. The password comes
// from the VALKEY_PASSWORD app secret at boot, so it never appears in the
// deployment row; the shell only expands that one variable.
func ManagedValkeyServerCommand(memoryMB int, policy string) []string {
	args := []string{
		"valkey-server",
		"--port", strconv.Itoa(ManagedValkeyPort),
		"--bind", "0.0.0.0",
		"--protected-mode", "no",
		"--save", "''",
		"--appendonly", "no",
		"--maxmemory", strconv.Itoa(memoryMB) + "mb",
		"--maxmemory-policy", policy,
		"--requirepass", `"$` + ManagedValkeyPasswordEnv + `"`,
	}
	for _, command := range ManagedValkeyBlockedCommands {
		args = append(args, "--rename-command", command, "''")
	}
	script := "exec"
	for _, arg := range args {
		script += " " + arg
	}
	return []string{"/bin/sh", "-c", script}
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

// BindRedisCacheRequest is POST /v1/redis/{name}/bindings.
type BindRedisCacheRequest struct {
	App    string `json:"app"`
	EnvKey string `json:"env_key,omitempty"`
}

// RedisCacheBinding is the result of a binding.
type RedisCacheBinding struct {
	Cache  string `json:"cache"`
	App    string `json:"app"`
	EnvKey string `json:"env_key"`
}
