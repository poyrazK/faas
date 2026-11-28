package main

// `gregale redis cli NAME [--app APP] [--] COMMAND [ARG...]` sends one command
// to a managed cache (ADR-20261116192126). Caches have no public address, so
// the command runs in a task VM of an app bound to the cache: guest-init's
// Redis built-in sends it through the private service proxy, which authorizes
// the binding and refuses the commands customers may not send, as for the
// app's own connections. No Redis client is needed in the app's image.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	redisCLIUsage = "usage: gregale redis exec NAME [--app APP] [--] COMMAND [ARG...]"
	// redisCLITaskTimeoutSeconds bounds a blocking command (BLPOP and the
	// like) on the server.
	redisCLITaskTimeoutSeconds = 120
	redisCLIWaitDefault        = 5 * time.Minute
	redisCLIPollInterval       = time.Second
)

// redisCLIArgs is a parsed `redis cli` command line.
type redisCLIArgs struct {
	cache, app  string
	waitTimeout time.Duration
	command     []string
}

// parseRedisCLIArgs reads NAME and the CLI's own flags, then takes the rest as
// the Redis command. The command starts at the first word after NAME that is
// not one of the CLI's flags, or after "--", so arguments such as `-1` in
// `LRANGE key 0 -1` are passed through.
func parseRedisCLIArgs(args []string) (redisCLIArgs, error) {
	out := redisCLIArgs{waitTimeout: redisCLIWaitDefault}
	value := func(i *int, name string) (string, error) {
		arg := args[*i]
		if v, ok := strings.CutPrefix(arg, "--"+name+"="); ok {
			return v, nil
		}
		if *i+1 >= len(args) {
			return "", fmt.Errorf("--%s needs a value", name)
		}
		*i++
		return args[*i], nil
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			out.command = args[i+1:]
			i = len(args)
		case arg == "--app" || strings.HasPrefix(arg, "--app="):
			v, err := value(&i, "app")
			if err != nil {
				return out, err
			}
			out.app = v
		case arg == "--wait-timeout" || strings.HasPrefix(arg, "--wait-timeout="):
			v, err := value(&i, "wait-timeout")
			if err != nil {
				return out, err
			}
			d, err := time.ParseDuration(v)
			if err != nil || d <= 0 {
				return out, fmt.Errorf("--wait-timeout %q is not a positive duration", v)
			}
			out.waitTimeout = d
		case out.cache == "":
			if strings.HasPrefix(arg, "-") {
				return out, fmt.Errorf("unknown flag %s", arg)
			}
			out.cache = arg
		default:
			out.command = args[i:]
			i = len(args)
		}
	}
	if out.cache == "" || len(out.command) == 0 {
		return out, errors.New("name a cache and a command")
	}
	return out, nil
}

func cmdRedisCLI(args []string) int {
	parsed, err := parseRedisCLIArgs(args)
	if err != nil {
		PrintUsage(os.Stderr, redisCLIUsage, "redis")
		return printErr("Invalid redis cli command", err)
	}
	request := api.CreateAppTaskRequest{
		Command:        api.ManagedValkeyCLICommand(parsed.cache, parsed.command),
		TimeoutSeconds: redisCLITaskTimeoutSeconds,
	}
	if _, problem := request.Resolve(); problem != nil {
		return printErr("Invalid redis cli command", problem)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	interrupt, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(interrupt, parsed.waitTimeout)
	defer cancel()
	cache, err := client.GetRedisCache(ctx, parsed.cache)
	if err != nil {
		return printErr("Could not find Redis cache", redisCacheNotFound(parsed.cache, err))
	}
	app, err := redisCLIApp(cache, parsed.app)
	if err != nil {
		return printErr("No app to run the command through", err)
	}
	task, err := client.CreateAppTask(ctx, app, request)
	if err != nil {
		return printErr("Could not run the command", err)
	}
	for !task.Status.Terminal() {
		select {
		case <-ctx.Done():
			if errors.Is(interrupt.Err(), context.Canceled) {
				if _, cancelErr := client.CancelAppTask(context.Background(), app, task.ID); cancelErr != nil {
					PrintWarn(osStderr, "could not cancel task %s: %v", task.ID, cancelErr)
				}
				return 130
			}
			return printErr("The command is still running", fmt.Errorf("gave up waiting after %s; see `gregale app tasks show %s %s`", parsed.waitTimeout, app, task.ID))
		case <-time.After(redisCLIPollInterval):
		}
		if task, err = client.GetAppTask(ctx, app, task.ID); err != nil {
			return printErr("Could not read the command's result", err)
		}
	}
	if jsonOutput {
		if err := writeJSON(task); err != nil {
			return jsonOut(err)
		}
		return appTaskExitCode(task)
	}
	return renderRedisCLITask(task, app)
}

// redisCLIApp is the app the command runs through: --app, or the cache's only
// bound app.
func redisCLIApp(cache api.RedisCache, app string) (string, error) {
	if app != "" {
		return app, nil
	}
	switch len(cache.BoundApps) {
	case 0:
		return "", fmt.Errorf("no app is bound to %s; the command runs through a bound app. Bind one with `gregale redis bind %s --app APP`", cache.Name, cache.Name)
	case 1:
		return cache.BoundApps[0], nil
	}
	return "", fmt.Errorf("%s is bound to %s; pass --app to choose one", cache.Name, strings.Join(cache.BoundApps, ", "))
}

// renderRedisCLITask prints the reply as redis-cli would, and explains a task
// that never reached the cache.
func renderRedisCLITask(task api.AppTaskResponse, app string) int {
	if task.StdoutTail != "" {
		_, _ = fmt.Fprint(osStdout, task.StdoutTail)
	}
	if task.StderrTail != "" {
		_, _ = fmt.Fprint(osStderr, task.StderrTail)
	}
	if task.Failure != nil {
		switch task.Failure.Code {
		case "command_not_found":
			// guest-init in the app's deployment predates the built-in.
			PrintFail(osStderr, "%s runs a runtime from before `gregale redis exec`; redeploy it (gregale deploy --name %s) and retry.", app, app)
		case "redis_unreachable":
			// guest-init already printed what to check.
		default:
			PrintFail(osStderr, "%s: %s", task.Failure.Code, task.Failure.Message)
		}
	} else if task.Status != api.AppTaskStatusSucceeded && task.Status != api.AppTaskStatusFailed {
		PrintFail(osStderr, "The command ended with status %s.", task.Status)
	}
	return appTaskExitCode(task)
}
