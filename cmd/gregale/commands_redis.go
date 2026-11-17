package main

// Managed Redis-compatible caches on Valkey.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const redisUsage = "usage: gregale redis <list|create|get|bind|restart|delete>"

func cmdRedis(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, redisUsage, "redis")
		return 1
	}
	switch args[0] {
	case subList:
		return cmdRedisList(args[1:])
	case subCreate:
		return cmdRedisCreate(args[1:])
	case subGet:
		return cmdRedisGet(args[1:])
	case "bind":
		return cmdRedisBind(args[1:])
	case "restart":
		return cmdRedisRestart(args[1:])
	case "delete":
		return cmdRedisDelete(args[1:])
	default:
		PrintUsage(os.Stderr, redisUsage, "redis")
		return 1
	}
}

func cmdRedisList(args []string) int {
	fs := newFlagSet("redis list", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale redis list", "redis")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	list, err := client.ListRedisCaches(context.Background())
	if err != nil {
		return printErr("Could not list Redis caches", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(list))
	}
	if len(list.Caches) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No Redis caches (your plan includes %d). Create one with `gregale redis create NAME`.\n", list.Limit)
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, "%-24s %8s  %-14s %s\n", "NAME", "MEMORY", "EVICTION", "HOST")
	for _, cache := range list.Caches {
		_, _ = fmt.Fprintf(osStdout, "%-24s %6dMB  %-14s %s:%d\n", cache.Name, cache.MemoryMB, cache.EvictionPolicy, cache.Host, cache.Port)
	}
	_, _ = fmt.Fprintf(osStdout, "%d of %d caches\n", len(list.Caches), list.Limit)
	return 0
}

func cmdRedisCreate(args []string) int {
	const usage = "usage: gregale redis create NAME [--memory MB] [--eviction-policy POLICY] [--idle-timeout SECONDS]"
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("redis create", flag.ContinueOnError)
	memory := fs.Int("memory", 0, "data size in MB, a multiple of 32 up to 512 (default 128)")
	policy := fs.String("eviction-policy", "", "maxmemory policy (default allkeys-lru)")
	idle := fs.Int("idle-timeout", 0, "seconds without traffic before the cache parks (default 900)")
	if err := fs.Parse(append(flags, positional...)); err != nil {
		return 1
	}
	if fs.NArg() != 1 || strings.TrimSpace(fs.Arg(0)) == "" {
		PrintUsage(os.Stderr, usage, "redis")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	cache, err := client.CreateRedisCache(context.Background(), api.CreateRedisCacheRequest{
		Name: fs.Arg(0), MemoryMB: *memory, EvictionPolicy: *policy, IdleTimeoutS: *idle,
	})
	if err != nil {
		return printErr("Could not create Redis cache", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(cache))
	}
	_, _ = fmt.Fprintf(osStdout, "Created Redis cache %s (%d MB, %s) at %s:%d\n", cache.Name, cache.MemoryMB, cache.EvictionPolicy, cache.Host, cache.Port)
	_, _ = fmt.Fprintf(osStdout, "Connect an app: gregale redis bind %s --app APP\n", cache.Name)
	return 0
}

func cmdRedisGet(args []string) int {
	fs := newFlagSet("redis get", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale redis get NAME", "redis")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	cache, err := client.GetRedisCache(context.Background(), fs.Arg(0))
	if err != nil {
		return printErr("Could not find Redis cache", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(cache))
	}
	_, _ = fmt.Fprintf(osStdout, "Name:      %s\nEngine:    %s\nHost:      %s:%d\nMemory:    %d MB\nEviction:  %s\nIdle park: %ds\nStatus:    %s\n",
		cache.Name, cache.Engine, cache.Host, cache.Port, cache.MemoryMB, cache.EvictionPolicy, cache.IdleTimeoutS, cache.Status)
	return 0
}

func cmdRedisBind(args []string) int {
	const usage = "usage: gregale redis bind NAME --app APP [--env KEY]"
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("redis bind", flag.ContinueOnError)
	app := fs.String("app", "", "app that receives the connection URL")
	envKey := fs.String("env", "", "environment variable to set (default REDIS_URL)")
	if err := fs.Parse(append(flags, positional...)); err != nil {
		return 1
	}
	if fs.NArg() != 1 || *app == "" {
		PrintUsage(os.Stderr, usage, "redis")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	binding, err := client.BindRedisCache(context.Background(), fs.Arg(0), api.BindRedisCacheRequest{App: *app, EnvKey: *envKey})
	if err != nil {
		return printErr("Could not bind Redis cache", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(binding))
	}
	_, _ = fmt.Fprintf(osStdout, "Set %s on %s; it takes effect at the app's next deploy or wake.\n", binding.EnvKey, binding.App)
	return 0
}

func cmdRedisRestart(args []string) int {
	fs := newFlagSet("redis restart", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale redis restart NAME", "redis")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.RestartRedisCache(context.Background(), fs.Arg(0))
	if err != nil {
		return printErr("Could not restart Redis cache", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Restarting Redis cache %s; its data is kept (wake %s).\n", fs.Arg(0), out.WakeID)
	return 0
}

func cmdRedisDelete(args []string) int {
	flags, positional := splitArgsForFlags(args, "yes")
	fs := newFlagSet("redis delete", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "confirm deletion without prompting")
	if err := fs.Parse(append(flags, positional...)); err != nil {
		return 1
	}
	if fs.NArg() != 1 || strings.TrimSpace(fs.Arg(0)) == "" {
		PrintUsage(os.Stderr, "usage: gregale redis delete NAME [--yes]", "redis")
		return 1
	}
	if code := requireAutomationConfirmation(*yes, "--yes"); code != 0 {
		return code
	}
	if !*yes && jsonOutput {
		return printErr("Confirmation required", errors.New("redis delete requires --yes in JSON mode"))
	}
	name := fs.Arg(0)
	if !*yes {
		_, _ = fmt.Fprintf(osStderr, "Delete Redis cache %q and all its data?\n", name)
		if !requireTyped(name) {
			return 130
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.DeleteRedisCache(context.Background(), name); err != nil {
		return printErr("Could not delete Redis cache", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"name": name, "deleted": true}))
	}
	_, _ = fmt.Fprintf(osStdout, "Deleted Redis cache %s\n", name)
	return 0
}
