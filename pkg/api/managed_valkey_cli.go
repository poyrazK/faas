package api

import (
	"net"
	"slices"
	"strconv"
	"strings"
)

// AppTaskRedisCommand is a reserved argv[0] that guest-init handles itself
// (ADR-20261116192126, `gregale redis cli`): it sends one command to a
// managed cache from inside the app's network and prints the reply. The
// customer image needs no Redis client. argv is
//
//	__gregale_redis_v1__ <cache>.svc.gregale:6379 COMMAND [ARG...]
//
// It runs only as a non-interactive task. The service proxy authorizes the
// app's binding and refuses the commands customers may not send, exactly as
// for the app's own connections.
const AppTaskRedisCommand = "__gregale_redis_v1__"

// ManagedValkeyCLIMaxReplyBytes bounds the reply guest-init reads and prints.
const ManagedValkeyCLIMaxReplyBytes = 1 << 20

// managedValkeyCLIStreamingCommands never finish with one reply, so a
// one-shot command cannot run them.
var managedValkeyCLIStreamingCommands = []string{
	"SUBSCRIBE", "PSUBSCRIBE", "SSUBSCRIBE", "MONITOR", "SYNC", "PSYNC",
}

// IsAppTaskRedisCommand reports whether command names the Redis built-in,
// valid or not.
func IsAppTaskRedisCommand(command []string) bool {
	return len(command) > 0 && command[0] == AppTaskRedisCommand
}

// ValidAppTaskRedisCommand checks the built-in's shape: not shell-interpreted,
// not interactive, a managed cache address, and a command that answers once.
func ValidAppTaskRedisCommand(command []string, shell, interactive bool) bool {
	if shell || interactive || len(command) < 3 || !validManagedValkeyCLITarget(command[1]) {
		return false
	}
	name := strings.ToUpper(strings.TrimSpace(command[2]))
	return name != "" && !slices.Contains(managedValkeyCLIStreamingCommands, name)
}

// ManagedValkeyCLICommand is the task argv that sends args to the cache.
func ManagedValkeyCLICommand(cache string, args []string) []string {
	return append([]string{AppTaskRedisCommand, ManagedValkeyAddr(cache)}, args...)
}

// validManagedValkeyCLITarget accepts only <cache>.svc.gregale:6379.
func validManagedValkeyCLITarget(target string) bool {
	host, port, err := net.SplitHostPort(target)
	if err != nil || port != strconv.Itoa(ManagedValkeyPort) {
		return false
	}
	name, ok := strings.CutSuffix(host, "."+ManagedValkeyServiceDomain)
	return ok && ValidAppSlug(name)
}
