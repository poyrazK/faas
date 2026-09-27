package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	flowBatchSize    = 128
	flowQueueSize    = 4096
	flowFlushEvery   = 100 * time.Millisecond
	flowRetryDelay   = time.Second
	flowConntrackBin = "/usr/sbin/conntrack"
)

type flowOwnerLookup interface {
	LookupFlowOwner(hostIP string, eventAt time.Time) (fcvm.FlowOwner, bool)
}

type flowEventSink interface {
	InsertOutboundFlowEvents(context.Context, []state.OutboundFlowEvent) error
}

type flowCaptureSink interface {
	flowEventSink
	flowCoverageSink
}

type conntrackTuple struct {
	protocol, sourceIP, destinationIP string
	sourcePort, destinationPort       uint16
	eventAt                           time.Time
}

func conntrackEventTime(field string) (time.Time, bool) {
	if len(field) < 4 || field[0] != '[' || field[len(field)-1] != ']' {
		return time.Time{}, false
	}
	seconds, fractional, ok := strings.Cut(field[1:len(field)-1], ".")
	if !ok || len(fractional) == 0 || len(fractional) > 9 {
		return time.Time{}, false
	}
	sec, err := strconv.ParseInt(seconds, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	nsec, err := strconv.ParseInt(fractional+strings.Repeat("0", 9-len(fractional)), 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(sec, nsec).UTC(), true
}

// parseConntrackNew accepts only the original tuple of a TCP/UDP NEW event.
// Conntrack prints the reply tuple as a second set of identical keys; the
// first values are the guest's source and its intended destination.
func parseConntrackNew(line string) (conntrackTuple, bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return conntrackTuple{}, false
	}
	eventAt, ok := conntrackEventTime(fields[0])
	if !ok {
		return conntrackTuple{}, false
	}
	newEvent := false
	protocol := ""
	for _, field := range fields {
		if field == "[NEW]" || field == "NEW" {
			newEvent = true
		}
		if protocol == "" && (field == "tcp" || field == "udp") {
			protocol = field
		}
	}
	if !newEvent || protocol == "" {
		return conntrackTuple{}, false
	}
	values := map[string]string{}
	for _, field := range fields {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		if (key == "src" || key == "dst" || key == "sport" || key == "dport") && values[key] == "" {
			values[key] = value
		}
	}
	src, err := netip.ParseAddr(values["src"])
	if err != nil {
		return conntrackTuple{}, false
	}
	dst, err := netip.ParseAddr(values["dst"])
	if err != nil {
		return conntrackTuple{}, false
	}
	sport, err := strconv.ParseUint(values["sport"], 10, 16)
	if err != nil {
		return conntrackTuple{}, false
	}
	dport, err := strconv.ParseUint(values["dport"], 10, 16)
	if err != nil {
		return conntrackTuple{}, false
	}
	return conntrackTuple{protocol, src.String(), dst.String(), uint16(sport), uint16(dport), eventAt}, true
}

// runOutboundFlowCapture listens to the host conntrack event stream, separate
// from the legacy 10-second, 32-summary capacity snapshot. Failure never
// blocks guest traffic. Every loss path emits a coverage_gap log record so
// operators cannot interpret missing rows as proof of no activity.
func runOutboundFlowCapture(ctx context.Context, owners flowOwnerLookup, sink flowCaptureSink, nodeID string, log *slog.Logger) {
	if owners == nil || sink == nil || nodeID == "" {
		return
	}
	if log == nil {
		log = slog.Default()
	}
	coverage := newFlowCaptureCoverage(sink, nodeID, log)
	coverageCtx, stopCoverage := context.WithCancel(context.WithoutCancel(ctx))
	go coverage.run(coverageCtx)
	queue := make(chan state.OutboundFlowEvent, flowQueueSize)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		writeOutboundFlows(ctx, sink, queue, log, coverage)
	}()
	defer func() {
		close(queue)
		<-writerDone
		stopCoverage()
		<-coverage.done
	}()
	for ctx.Err() == nil {
		err := captureOutboundFlowProcess(ctx, owners, nodeID, queue, log, coverage)
		if ctx.Err() != nil {
			return
		}
		log.Warn("outbound flow capture coverage_gap", "reason", "collector_stopped", "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(flowRetryDelay):
		}
	}
}

func captureOutboundFlowProcess(ctx context.Context, owners flowOwnerLookup, nodeID string, queue chan<- state.OutboundFlowEvent, log *slog.Logger, coverage *flowCaptureCoverage) error {
	cmd := exec.CommandContext(ctx, flowConntrackBin, "-E", "-e", "NEW", "-o", "timestamp,extended", "-b", "1048576")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	coverage.setListening(true, "listening")
	defer coverage.setListening(false, "collector_stopped")
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		scan := bufio.NewScanner(stderr)
		for scan.Scan() {
			line := scan.Text()
			if len(line) > 256 {
				line = line[:256]
			}
			log.Warn("outbound flow capture coverage_gap", "reason", "conntrack_stderr", "detail", line)
			coverage.stderrEvent()
		}
	}()
	log.Info("outbound flow capture started", "node_id", nodeID)
	scan := bufio.NewScanner(stdout)
	scan.Buffer(make([]byte, 4096), 64*1024)
	dropped := 0
	unparsed := 0
	for scan.Scan() {
		line := scan.Text()
		tuple, ok := parseConntrackNew(line)
		if !ok {
			if strings.Contains(line, "[NEW]") && (strings.Contains(line, " tcp ") || strings.Contains(line, " udp ")) {
				unparsed++
				coverage.unparsedEvent()
				if unparsed == 1 || unparsed%1000 == 0 {
					log.Warn("outbound flow capture coverage_gap", "reason", "unparsed_event", "count", unparsed)
				}
			}
			continue
		}
		owner, ok := owners.LookupFlowOwner(tuple.sourceIP, tuple.eventAt)
		if !ok {
			// Host-generated and incoming connections are not guest egress.
			continue
		}
		event := state.OutboundFlowEvent{
			ID: uuid.NewString(), ObservedAt: tuple.eventAt, NodeID: nodeID, InstanceID: owner.InstanceID,
			AccountID: owner.AccountID, AppID: owner.AppID, DeploymentID: owner.DeploymentID,
			SourceIP: tuple.sourceIP, SourcePort: tuple.sourcePort,
			DestinationIP: tuple.destinationIP, DestinationPort: tuple.destinationPort,
			Protocol: tuple.protocol,
		}
		select {
		case queue <- event:
		default:
			dropped++
			coverage.queueDrop()
			if dropped == 1 || dropped%1000 == 0 {
				log.Warn("outbound flow capture coverage_gap", "reason", "queue_full", "dropped", dropped)
			}
		}
	}
	scanErr := scan.Err()
	waitErr := cmd.Wait()
	<-stderrDone
	if scanErr != nil {
		return fmt.Errorf("read conntrack events: %w", scanErr)
	}
	if waitErr != nil {
		return waitErr
	}
	return errors.New("conntrack event stream ended")
}

func writeOutboundFlows(ctx context.Context, sink flowEventSink, queue <-chan state.OutboundFlowEvent, log *slog.Logger, coverage *flowCaptureCoverage) {
	tick := time.NewTicker(flowFlushEvery)
	defer tick.Stop()
	batch := make([]state.OutboundFlowEvent, 0, flowBatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		for attempt := 1; attempt <= 3; attempt++ {
			writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := sink.InsertOutboundFlowEvents(writeCtx, batch)
			cancel()
			if err == nil {
				batch = batch[:0]
				return
			}
			if attempt == 3 || ctx.Err() != nil {
				coverage.databaseDrop(int64(len(batch)), "database_write_failed")
				log.Warn("outbound flow capture coverage_gap", "reason", "database_write_failed", "dropped", len(batch), "err", err)
				batch = batch[:0]
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(flowRetryDelay):
			}
		}
	}
	done := ctx.Done()
	draining := false
	for {
		select {
		case <-done:
			// The collector closes queue after the conntrack process exits.
			// Drain its remaining events so the final coverage sample counts
			// every queued event that cannot be written during shutdown.
			done = nil
			draining = true
		case event, ok := <-queue:
			if !ok {
				if draining {
					if len(batch) > 0 {
						coverage.databaseDrop(int64(len(batch)), "shutdown_queue")
						log.Warn("outbound flow capture coverage_gap", "reason", "shutdown_queue", "dropped", len(batch))
					}
				} else {
					flush()
				}
				return
			}
			batch = append(batch, event)
			if len(batch) == flowBatchSize && !draining {
				flush()
			}
		case <-tick.C:
			if !draining {
				flush()
			}
		}
	}
}
