//go:build linux

package objectqualification

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
)

type agentInit struct {
	Directory, Daemon, Control, Key, Secret string
	Workers                                 int
	WorkBytes                               int64
	WorkEntries                             int
	AutomaticCompaction                     bool
	CompactionRecords                       int64
}

type agentRequest struct {
	Operation string                       `json:"operation"`
	Init      *agentInit                   `json:"init,omitempty"`
	Path      string                       `json:"path,omitempty"`
	Data      []byte                       `json:"data,omitempty"`
	Offset    int64                        `json:"offset,omitempty"`
	Floor     int64                        `json:"floor,omitempty"`
	Source    string                       `json:"source,omitempty"`
	Mode      string                       `json:"mode,omitempty"`
	Killed    bool                         `json:"killed,omitempty"`
	Status    objectnode.ReplicationStatus `json:"status,omitempty"`
}

type AgentIdentity struct {
	ProtocolVersion  int    `json:"protocol_version"`
	KernelBootSHA256 string `json:"kernel_boot_sha256"`
	DaemonSHA256     string `json:"daemon_sha256"`
	ControlSHA256    string `json:"control_sha256"`
	AgentSHA256      string `json:"agent_sha256"`
	Platform         string `json:"platform"`
	FilesystemType   int64  `json:"filesystem_type"`
	Address          string `json:"address"`
	Metrics          string `json:"metrics"`
}

type agentResponse struct {
	Error     string                       `json:"error,omitempty"`
	Identity  *AgentIdentity               `json:"identity,omitempty"`
	Status    objectnode.ReplicationStatus `json:"status,omitempty"`
	Sample    *agentSample                 `json:"sample,omitempty"`
	Path      string                       `json:"path,omitempty"`
	Size      int64                        `json:"size,omitempty"`
	Directory bool                         `json:"directory,omitempty"`
	Data      []byte                       `json:"data,omitempty"`
	Done      bool                         `json:"done,omitempty"`
}

type agentState struct {
	lab         *lab
	identity    AgentIdentity
	files       []transferFile
	current     transferFile
	importBytes int64
	importFiles int
}

// ServeAgent is a private stdio protocol executed by OpenSSH. It accepts only a
// fresh root, fixed node operations and bounded backup chunks, never shell argv.
// EOF, cancellation and protocol failures stop and reap all managed processes.
func ServeAgent(ctx context.Context, input io.Reader, output io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	operationCtx, cancelOperation := context.WithCancel(ctx)
	defer cancelOperation()
	if closer, ok := input.(io.Closer); ok {
		defer func() { _ = closer.Close() }()
	}
	state := &agentState{}
	defer func() {
		if state.lab != nil {
			state.lab.close(ctx)
		}
	}()
	lines := make(chan []byte)
	readErr := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, api.ObjectQualificationAgentChunkBytes), api.ObjectQualificationAgentLineBytes)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			select {
			case lines <- line:
			case <-ctx.Done():
				return
			}
		}
		cancelOperation() // EOF also interrupts an operation already in progress.
		readErr <- scanner.Err()
	}()
	encoder := json.NewEncoder(output)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-readErr:
			return err
		case line := <-lines:
			var request agentRequest
			if err := json.Unmarshal(line, &request); err != nil {
				return errors.New("invalid qualification agent message")
			}
			result, err := state.handle(operationCtx, request)
			if err != nil {
				result.Error = err.Error()
			}
			if err = encoder.Encode(result); err != nil {
				return err
			}
			if request.Operation == "close" {
				return nil
			}
		}
	}
}

func (s *agentState) initialize(in *agentInit) (agentResponse, error) {
	if s.lab != nil || in == nil || !filepath.IsAbs(in.Directory) || !filepath.IsAbs(in.Daemon) || !filepath.IsAbs(in.Control) || in.Key == "" || in.Secret == "" {
		return agentResponse{}, errors.New("invalid or repeated qualification agent initialization")
	}
	o := DefaultOptions()
	o.Directory, o.Daemon, o.Control, o.Workers = in.Directory, in.Daemon, in.Control, in.Workers
	o.WorkBytesPerSecond, o.WorkEntriesPerSecond = in.WorkBytes, in.WorkEntries
	o.AutomaticCompaction = in.AutomaticCompaction
	if in.CompactionRecords != 0 {
		o.CompactionRecords = in.CompactionRecords
	}
	if err := o.validate(); err != nil {
		return agentResponse{}, err
	}
	identity, err := identifyAgent(o)
	if err != nil {
		return agentResponse{}, err
	}
	if err = os.Mkdir(in.Directory, 0o700); err != nil {
		return agentResponse{}, err
	}
	var fs syscall.Statfs_t
	if err = syscall.Statfs(in.Directory, &fs); err != nil {
		return agentResponse{}, err
	}
	identity.FilesystemType = int64(fs.Type)
	l := &lab{options: o, root: in.Directory, key: in.Key, secret: in.Secret}
	l.primaryDir, l.followerDir = filepath.Join(l.root, "node"), filepath.Join(l.root, "node")
	s.lab, s.identity = l, identity
	return agentResponse{Identity: &identity}, nil
}

func identifyAgent(o Options) (AgentIdentity, error) {
	i := AgentIdentity{ProtocolVersion: 2, Platform: runtime.GOOS + "/" + runtime.GOARCH}
	var err error
	if i.KernelBootSHA256, err = kernelBootDigest(); err != nil {
		return i, err
	}
	if i.DaemonSHA256, err = binaryDigest(o.Daemon); err != nil {
		return i, err
	}
	if i.ControlSHA256, err = binaryDigest(o.Control); err != nil {
		return i, err
	}
	executable, err := os.Executable()
	if err != nil {
		return i, err
	}
	if i.AgentSHA256, err = binaryDigest(executable); err != nil {
		return i, err
	}
	i.Address, i.Metrics, err = loopbackAddresses()
	return i, err
}

func (s *agentState) handle(ctx context.Context, r agentRequest) (agentResponse, error) {
	if r.Operation == "init" {
		return s.initialize(r.Init)
	}
	if s.lab == nil {
		return agentResponse{}, errors.New("qualification agent is not initialized")
	}
	l := s.lab
	var status objectnode.ReplicationStatus
	var err error
	switch r.Operation {
	case "close":
		err = errors.Join(l.stopFollower(ctx, false), l.stopPrimary(ctx, false))
	case "put", "mkdir", "read", "next", "export":
		return s.transfer(ctx, r)
	case "bootstrap":
		_, err = objectnode.BootstrapReplica(ctx, filepath.Join(l.root, "import"), l.followerDir)
		if err == nil {
			status, err = objectnode.ReadReplicationStatus(l.followerDir)
		}
	case "status":
		status, err = objectnode.ReadReplicationStatus(l.followerDir)
	case "compact":
		_, err = objectnode.CompactJournal(ctx, l.followerDir, -1)
	case "register":
		status, err = objectnode.RegisterFollower(ctx, l.primaryDir, r.Status.NodeID, r.Status.AppliedSequence, r.Status.AppliedHash)
		if err == nil && r.Mode == objectnode.DurabilityReplica {
			status, err = objectnode.ConfigureDurability(ctx, l.primaryDir, r.Mode, r.Status.NodeID)
		}
	case "start-primary":
		if l.primary != nil {
			err = errors.New("primary already running")
			break
		}
		l.primary, err = l.spawn(ctx, l.options.Daemon, "primary", "--data-dir", l.primaryDir, "--listen", s.identity.Address, "--metrics-listen", s.identity.Metrics, "--scrub=false", "--min-free-bytes", strconv.FormatInt(r.Floor, 10), "--max-concurrent-writes", strconv.Itoa(l.options.Workers+1), "--compact="+strconv.FormatBool(l.options.AutomaticCompaction), "--compact-records", strconv.FormatInt(l.options.CompactionRecords, 10))
	case "start-follower":
		if l.follower != nil {
			err = errors.New("follower already running")
			break
		}
		l.follower, err = l.spawn(ctx, l.options.Control, "follower", l.followerArgs(r.Source)...)
	case "stop-primary":
		err = l.stopPrimary(ctx, r.Killed)
	case "stop-follower":
		err = l.stopFollower(ctx, r.Killed)
	case "health-follower":
		err = childHealthy(l.follower)
	case "sample":
		return s.sample()
	case "fence", "catch-up", "promote":
		return s.authority(ctx, r)
	default:
		err = fmt.Errorf("unknown qualification agent operation %q", r.Operation)
	}
	return agentResponse{Status: status}, err
}

func (s *agentState) authority(ctx context.Context, r agentRequest) (agentResponse, error) {
	l := s.lab
	source, err := objectnode.NewHTTPReplicationSource(r.Source, "us-east-1", l.key, l.secret)
	if err != nil {
		return agentResponse{}, err
	}
	switch r.Operation {
	case "fence":
		_, err = objectnode.FenceReplicaPrimary(ctx, l.followerDir, source)
	case "promote":
		_, err = objectnode.PromoteReplica(ctx, l.followerDir, source)
	case "catch-up":
		var progress objectnode.ReplicationProgress
		progress, err = objectnode.CatchUpReplica(ctx, l.followerDir, source, l.catchUpOptions())
		return agentResponse{Done: progress.CaughtUp}, err
	}
	return agentResponse{}, err
}

func childHealthy(c *child) error {
	if c == nil {
		return errors.New("qualification child is not running")
	}
	select {
	case <-c.done:
		return errors.New("qualification child exited; inspect private logs")
	default:
		return nil
	}
}
