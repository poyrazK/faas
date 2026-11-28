//go:build linux

package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

const redisCLIDialTimeout = 10 * time.Second

func isRedisCLICommand(req apptaskproto.Request) bool {
	return api.IsAppTaskRedisCommand(req.Command)
}

// executeRedisCLICommand sends one command to a managed cache and prints the
// reply. An error reply exits 1, like a failed command.
func executeRedisCLICommand(ctx context.Context, req apptaskproto.Request, stdout io.Writer) (apptaskproto.Result, error) {
	if !api.ValidAppTaskRedisCommand(req.Command, req.CommandShell, false) {
		return appTaskInfraFailure("builtin_invalid", "the redis command was called with invalid arguments", 2), nil
	}
	target := req.Command[1]
	failed := func(err error) (apptaskproto.Result, error) {
		_, _ = io.WriteString(stdout, redisCLIConnectionHint(target, err))
		exit := 1
		return apptaskproto.Result{Status: apptaskproto.StatusFailed, ExitCode: &exit,
			FailureCode: "redis_unreachable", FailureMessage: "the cache did not answer"}, nil
	}
	dialer := &net.Dialer{Timeout: redisCLIDialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		return failed(err)
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if _, err := conn.Write(redisCLIRequest(req.Command[2:])); err != nil {
		return failed(err)
	}
	reply, err := readRedisCLIReply(conn, api.ManagedValkeyCLIMaxReplyBytes)
	if ctx.Err() != nil {
		return apptaskproto.Result{}, ctx.Err()
	}
	if err != nil {
		return failed(err)
	}
	if _, err := io.WriteString(stdout, formatRedisCLIReply(reply)); err != nil {
		return apptaskproto.Result{}, fmt.Errorf("write redis reply: %w", err)
	}
	exit := 0
	if reply.kind == '-' || reply.kind == '!' {
		exit = 1
		return apptaskproto.Result{Status: apptaskproto.StatusFailed, ExitCode: &exit}, nil
	}
	return apptaskproto.Result{Status: apptaskproto.StatusSucceeded, ExitCode: &exit}, nil
}
