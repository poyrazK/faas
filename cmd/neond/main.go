// Command neond holds Gregale's Neon API keys (ADR-20261112160000).
//
// apid's Neon backend forwards each Neon API request to neond over a unix
// socket. neond admits only the calls the backend makes, refuses deleting a
// live database's project or branch, rate-limits destructive calls, logs
// every call and sends it to Neon with its own key. Only the users named in
// FAAS_NEOND_CALLERS may connect.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"

	neondpb "github.com/onebox-faas/faas/api/proto/onebox/faas/neond/v1"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/neond"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/wire"
)

// neonAPIBase is Neon's API. neond never follows a redirect away from it.
const neonAPIBase = "https://console.neon.tech/api/v2"

// defaultDestructivePerHour bounds project, branch, role, endpoint and
// snapshot deletes forwarded per hour when FAAS_NEOND_DESTRUCTIVE_PER_HOUR is
// unset.
const defaultDestructivePerHour = 300

func main() {
	wire.Daemon("neond", run)
}

type config struct {
	listen, adminKeyFile, metricsAddr string
	callers                           []string
	destructivePerHour                int
}

func configFromEnv(getenv func(string) string) (config, error) {
	cfg := config{
		listen:             getenv("FAAS_NEOND_LISTEN"),
		adminKeyFile:       getenv("FAAS_NEOND_ADMIN_KEY_FILE"),
		metricsAddr:        getenv("FAAS_NEOND_METRICS_ADDR"),
		destructivePerHour: defaultDestructivePerHour,
	}
	target, err := wire.ParseTarget(cfg.listen)
	if err != nil || target.Scheme != wire.SchemeUnix {
		return config{}, fmt.Errorf("neond: FAAS_NEOND_LISTEN=%q: want unix:///run/faas/neond.sock", cfg.listen)
	}
	if cfg.adminKeyFile == "" {
		return config{}, errors.New("neond: FAAS_NEOND_ADMIN_KEY_FILE is required: neond reads the Neon admin key from a credential file, never the environment")
	}
	for _, name := range strings.Split(getenv("FAAS_NEOND_CALLERS"), ",") {
		if name = strings.TrimSpace(name); name != "" {
			cfg.callers = append(cfg.callers, name)
		}
	}
	if len(cfg.callers) == 0 {
		return config{}, errors.New("neond: FAAS_NEOND_CALLERS is required: the OS users allowed to connect (apid's)")
	}
	if raw := getenv("FAAS_NEOND_DESTRUCTIVE_PER_HOUR"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return config{}, fmt.Errorf("neond: FAAS_NEOND_DESTRUCTIVE_PER_HOUR=%q: want a positive integer", raw)
		}
		cfg.destructivePerHour = n
	}
	return cfg, nil
}

func run(ctx context.Context, log *slog.Logger) error {
	if err := role.Require("neond", role.FromConfig("", "FAAS_NEOND_ROLE"),
		role.RoleSingleBox, role.RoleControlPlane); err != nil {
		return err
	}
	cfg, err := configFromEnv(os.Getenv)
	if err != nil {
		return err
	}
	adminKey, err := readKey(cfg.adminKeyFile)
	if err != nil {
		return err
	}
	callers, err := callerUIDs(cfg.callers)
	if err != nil {
		return err
	}
	pool, err := db.OpenWithAppName(ctx, "", "faas-neond")
	if err != nil {
		return fmt.Errorf("neond: open database: %w", err)
	}
	defer pool.Close()

	base, err := url.Parse(neonAPIBase)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	reg := prometheus.NewRegistry()
	server, err := neond.NewServer(base, client, adminKey, pgLive{pool}, cfg.destructivePerHour, reg, log)
	if err != nil {
		return err
	}
	if err := serveMetrics(ctx, cfg.metricsAddr, reg, log); err != nil {
		return err
	}
	lis, err := wire.Listen(ctx, cfg.listen, nil)
	if err != nil {
		return fmt.Errorf("neond: listen %s: %w", cfg.listen, err)
	}
	lis = peerCheckedListener{Listener: lis, allowed: callers, log: log}
	srv := grpc.NewServer()
	neondpb.RegisterNeondServer(srv, server)
	go func() {
		<-ctx.Done()
		srv.GracefulStop()
	}()
	log.Info("neond: serving", "listen", cfg.listen, "callers", cfg.callers, "destructive_per_hour", cfg.destructivePerHour)
	if err := srv.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return fmt.Errorf("neond: serve: %w", err)
	}
	return nil
}

// readKey reads the admin key from its credential file. The key is never
// logged.
func readKey(path string) (string, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the operator names the credential file
	if err != nil {
		return "", fmt.Errorf("neond: read admin key: %w", err)
	}
	key := strings.TrimSpace(string(raw))
	if key == "" || strings.ContainsAny(key, " \t\r\n") {
		return "", errors.New("neond: admin key file is empty or malformed")
	}
	return key, nil
}

func callerUIDs(names []string) (map[int]bool, error) {
	uids := make(map[int]bool, len(names))
	for _, name := range names {
		u, err := user.Lookup(name)
		if err != nil {
			return nil, fmt.Errorf("neond: caller %q: %w", name, err)
		}
		uid, err := strconv.Atoi(u.Uid)
		if err != nil {
			return nil, fmt.Errorf("neond: caller %q: uid %q", name, u.Uid)
		}
		uids[uid] = true
	}
	return uids, nil
}

// peerCheckedListener closes connections from processes whose uid is not an
// allowed caller's, as the kernel recorded it at connect time.
type peerCheckedListener struct {
	net.Listener
	allowed map[int]bool
	log     *slog.Logger
}

func (l peerCheckedListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		uid, err := peerUID(conn)
		if err == nil && l.allowed[uid] {
			return conn, nil
		}
		l.log.Warn("neond: connection from a process that is not an allowed caller refused", "uid", uid, "err", err)
		_ = conn.Close()
	}
}

// pgLive reads the live databases in a Neon project.
type pgLive struct{ pool *pgxpool.Pool }

func (p pgLive) LiveResourcesInProject(ctx context.Context, project string) ([]string, error) {
	return sqlc.New().ManagedPostgresLiveResourcesInProject(ctx, p.pool, project)
}

func serveMetrics(ctx context.Context, addr string, reg *prometheus.Registry, log *slog.Logger) error {
	if addr == "" {
		return nil
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("neond: metrics listen %s: %w", addr, err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("neond: metrics server", "err", err)
		}
	}()
	return nil
}
