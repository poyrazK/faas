package main

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// `--boot --healthcheck-grpc` probes the app as Gregale's gRPC readiness does
// (pkg/fcvm waitReadyWithProbe): plaintext gRPC to $PORT, calling
// grpc.health.v1.Health/Check with the configured service (empty: the whole
// server), until it answers SERVING or the startup window ends.

// doctorBootGRPCProbe is the outcome of polling the health service.
type doctorBootGRPCProbe struct {
	serving bool
	// answers counts Check responses; last is the latest status among them.
	answers int
	last    healthpb.HealthCheckResponse_ServingStatus
	// unimplemented: the server answers gRPC without the health service.
	unimplemented bool
	// unknownService: the health server does not know the service (yet).
	unknownService bool
}

// grpcServiceLabel names what the probe asks about.
func (o doctorBootOptions) grpcServiceLabel() string {
	if o.grpcService == "" {
		return "the whole server"
	}
	return fmt.Sprintf("service %q", o.grpcService)
}

// checkGRPC boots a gRPC-health app.
func (b doctorBoot) checkGRPC(ctx context.Context) []doctorCheck {
	probe, exited, err := b.waitForGRPC(ctx)
	if err != nil {
		return []doctorCheck{{Name: "boot-start", Status: "error", Code: "boot_inspect_failed", Hint: err.Error()}}
	}
	if exited != nil {
		return []doctorCheck{b.exitedCheck(ctx, "boot-start", *exited)}
	}
	if !probe.serving && probe.answers == 0 && !probe.unimplemented && !probe.unknownService {
		return []doctorCheck{
			{Name: "boot-start", Status: "ok", Detail: "container running" + b.memoryNote()},
			b.listenerProblem(ctx),
		}
	}
	checks := []doctorCheck{
		{Name: "boot-start", Status: "ok", Detail: fmt.Sprintf("answered gRPC in %s", bootSeconds(time.Since(b.started))) + b.memoryNote()},
		{Name: "boot-listener", Status: "ok", Detail: fmt.Sprintf("listening on 0.0.0.0:%d ($PORT)", b.opts.port)},
		b.grpcHealthCheck(probe),
	}
	if !probe.serving {
		return checks
	}
	if b.opts.imageHealthcheck != nil {
		checks = append(checks, b.imageHealth(ctx))
	}
	return append(checks, b.stayUp(ctx))
}

func (b doctorBoot) grpcHealthCheck(p doctorBootGRPCProbe) doctorCheck {
	label := b.opts.grpcServiceLabel()
	switch {
	case p.serving:
		return doctorCheck{Name: "boot-grpc-health", Status: "ok", Detail: fmt.Sprintf("Health/Check for %s → SERVING", label)}
	case p.unimplemented:
		return doctorCheck{Name: "boot-grpc-health", Status: "error", Code: "boot_grpc_health_missing",
			Hint: "The app answers gRPC but does not implement grpc.health.v1.Health. Gregale's readiness probe calls Health/Check and would never see SERVING, so the deploy would fail.",
			Fix:  "Register the standard gRPC health service (grpc-go: google.golang.org/grpc/health NewServer; grpc-java: HealthStatusManager; Python: grpcio-health-checking) and set it to SERVING once the app can serve."}
	case p.answers > 0:
		return doctorCheck{Name: "boot-grpc-health", Status: "error", Code: "boot_grpc_health_failed",
			Hint: fmt.Sprintf("Health/Check for %s answered %d times, last %s, and never SERVING within %s. Gregale routes to an instance only once it answers SERVING.", label, p.answers, p.last, bootSeconds(doctorBootStartupTimeout)),
			Fix:  "Set the health status to SERVING once the app can serve, and check that the service name matches --healthcheck-grpc-service."}
	default:
		return doctorCheck{Name: "boot-grpc-health", Status: "error", Code: "boot_grpc_service_unknown",
			Hint: fmt.Sprintf("The health server does not know %s (NOT_FOUND) after %s.", label, bootSeconds(doctorBootStartupTimeout)),
			Fix:  "Register that service name with the health server and set it to SERVING, or pass the name the app registers with --healthcheck-grpc-service (empty checks the whole server)."}
	}
}

// waitForGRPC polls Health/Check until SERVING, the container exits, the
// server shows it lacks the health service, or the startup window ends.
func (b doctorBoot) waitForGRPC(ctx context.Context) (doctorBootGRPCProbe, *doctorBootState, error) {
	deadline := time.Now().Add(doctorBootStartupTimeout)
	var probe doctorBootGRPCProbe
	var conn *grpc.ClientConn
	defer func() {
		if conn != nil {
			_ = conn.Close()
		}
	}()
	for {
		st, err := b.docker.state(ctx, b.name)
		if err != nil {
			return probe, nil, err
		}
		if !st.running {
			return probe, &st, nil
		}
		if conn == nil {
			if addr := b.publishedAddr(ctx); addr != "" {
				conn, err = grpc.NewClient("passthrough:///"+addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
				if err != nil {
					return probe, nil, fmt.Errorf("create gRPC client: %w", err)
				}
			}
		}
		if conn != nil && b.grpcProbeOnce(ctx, conn, &probe) {
			return probe, nil, nil
		}
		if time.Now().After(deadline) {
			return probe, nil, nil
		}
		select {
		case <-ctx.Done():
			return probe, nil, ctx.Err()
		case <-time.After(doctorBootPoll):
		}
	}
}

// grpcProbeOnce makes one Check and records it; true ends the wait.
func (b doctorBoot) grpcProbeOnce(ctx context.Context, conn *grpc.ClientConn, probe *doctorBootGRPCProbe) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	resp, err := healthpb.NewHealthClient(conn).Check(probeCtx, &healthpb.HealthCheckRequest{Service: b.opts.grpcService})
	if err == nil {
		probe.answers++
		probe.last = resp.GetStatus()
		probe.serving = probe.last == healthpb.HealthCheckResponse_SERVING
		return probe.serving
	}
	switch status.Code(err) {
	case codes.Unimplemented:
		// No health service: registering one later is not something apps do.
		probe.unimplemented = true
		return true
	case codes.NotFound:
		// grpc-go's health server answers NOT_FOUND until the app sets
		// the service's status, which it may do once initialized.
		probe.unknownService = true
	}
	return false
}
