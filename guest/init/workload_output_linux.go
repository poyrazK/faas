//go:build linux

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// Each workload owns its output pipes, so /dev/stdout and /dev/stderr may
// be reopened after dropping credentials. Go's implicit io.Writer pipes are
// owned by guest-init (root), which breaks ordinary non-root OCI log links.
type workloadOutput struct{ streams []*workloadOutputStream }
type workloadOutputStream struct {
	reader, writer *os.File
	done           chan error
}

func prepareWorkloadOutput(cmd *exec.Cmd, sup *Supervisor) (*workloadOutput, error) {
	stdout, stderr := io.Writer(os.Stdout), io.Writer(os.Stderr)
	if sup != nil {
		stdout = io.MultiWriter(os.Stdout, sup.LogBuffer())
		stderr = io.MultiWriter(os.Stdout, sup.LogBuffer())
	}
	return prepareWorkloadOutputWithWriters(cmd, stdout, stderr)
}

func prepareWorkloadOutputWithWriters(cmd *exec.Cmd, stdout, stderr io.Writer) (*workloadOutput, error) {
	uid, gid := os.Getuid(), os.Getgid()
	if cmd.SysProcAttr != nil && cmd.SysProcAttr.Credential != nil {
		uid, gid = int(cmd.SysProcAttr.Credential.Uid), int(cmd.SysProcAttr.Credential.Gid)
	}
	output := &workloadOutput{}
	destinations := []io.Writer{stdout, stderr}
	for _, destination := range destinations {
		reader, writer, err := os.Pipe()
		if err != nil {
			output.close()
			return nil, fmt.Errorf("workload output pipe: %w", err)
		}
		if err := writer.Chown(uid, gid); err != nil {
			_ = reader.Close()
			_ = writer.Close()
			output.close()
			return nil, fmt.Errorf("workload output ownership: %w", err)
		}
		stream := &workloadOutputStream{reader: reader, writer: writer, done: make(chan error, 1)}
		output.streams = append(output.streams, stream)
		go func() { _, err := io.Copy(destination, reader); _ = reader.Close(); stream.done <- err }()
	}
	cmd.Stdout, cmd.Stderr = output.streams[0].writer, output.streams[1].writer
	return output, nil
}

// Call immediately after Start, including failed starts. Only the child keeps
// a write descriptor, allowing normal exit to finish draining the log stream.
func (o *workloadOutput) started() {
	for _, stream := range o.streams {
		_ = stream.writer.Close()
	}
}

func (o *workloadOutput) close() {
	o.started()
	// A detached descendant may retain a descriptor after the supervised child
	// exits. Bound cleanup and close the read end rather than leak a log copier.
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for _, stream := range o.streams {
		select {
		case <-stream.done:
		case <-deadline.C:
			for _, pending := range o.streams {
				_ = pending.reader.Close()
			}
			return
		}
	}
}
