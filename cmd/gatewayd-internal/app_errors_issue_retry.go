package main

import (
	"context"
	"time"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Retain only Issues occurrences: retrying the legacy stream can double-count.
func (p *appErrorsPublisher) queueHTTPIssueRetries(batch []appErrorRow) {
	p.issueMu.Lock()
	now := time.Now()
	for _, row := range batch {
		if row.HTTPStatus < 500 || row.DeploymentID == "" || row.RequestID == "" || row.IssueEventID == "" {
			continue
		}
		key := issueRetryKey(row)
		if p.issueKeys[key] {
			continue
		}
		if len(p.issueKeys) >= httpIssueRetryQueueLimit || p.issueApps[row.AppID] >= httpIssueRetryAppLimit {
			p.log.Warn("HTTP issue retry queue full", "app_id", row.AppID)
			p.ops.ObserveIssueEvent("retry_dropped")
			continue
		}
		p.issueQueue = append(p.issueQueue, httpIssueRetry{row: row, expires: now.Add(httpIssueRetryLifetime), next: now, delay: time.Second})
		p.issueApps[row.AppID]++
		p.issueKeys[key] = true
	}
	p.issueMu.Unlock()
	select {
	case p.issueWake <- struct{}{}:
	default:
	}
}

func issueRetryKey(row appErrorRow) string {
	return row.AppID + "\x00" + row.DeploymentID + "\x00" + row.IssueEventID
}

// runHTTPIssueRetries cannot block fresh legacy batches. The periodic wake
// handles backoff even when no new retries arrive.
func (p *appErrorsPublisher) runHTTPIssueRetries(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-p.issueWake:
		}
		p.retryHTTPIssues(ctx)
	}
}

// Unimplemented is retried for rolling apid upgrades.
func (p *appErrorsPublisher) retryHTTPIssues(parent context.Context) {
	client, ok := p.client.(httpIssueRetryClient)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	for i := 0; i < 32 && ctx.Err() == nil; i++ {
		p.issueMu.Lock()
		now := time.Now()
		index := -1
		for j, entry := range p.issueQueue {
			if !entry.next.After(now) || !entry.expires.After(now) {
				index = j
				break
			}
		}
		if index < 0 {
			p.issueMu.Unlock()
			return
		}
		entry := p.issueQueue[index]
		copy(p.issueQueue[index:], p.issueQueue[index+1:])
		p.issueQueue[len(p.issueQueue)-1] = httpIssueRetry{}
		p.issueQueue = p.issueQueue[:len(p.issueQueue)-1]
		p.issueMu.Unlock()

		var err error
		var response *apidpb.IncrementAppErrorResponse
		if entry.expires.After(now) {
			callCtx, callCancel := context.WithTimeout(ctx, 2*time.Second)
			response, err = client.RecordHTTPIssue(callCtx, appErrorRequest(entry.row))
			callCancel()
		} else {
			err = context.DeadlineExceeded
		}
		if err == nil && response.GetOutcome() != "accepted" && response.GetOutcome() != "duplicate" && response.GetOutcome() != "retry_skipped" {
			err = status.Error(codes.Unavailable, "invalid HTTP issue acknowledgement")
		}
		code := status.Code(err)
		retry := err != nil && entry.expires.After(time.Now()) && code != codes.InvalidArgument && code != codes.NotFound && code != codes.PermissionDenied && code != codes.AlreadyExists
		p.issueMu.Lock()
		if retry {
			entry.next = time.Now().Add(entry.delay)
			entry.delay = min(time.Minute, entry.delay*2)
			p.issueQueue = append(p.issueQueue, entry)
		} else {
			delete(p.issueKeys, issueRetryKey(entry.row))
			p.issueApps[entry.row.AppID]--
			if p.issueApps[entry.row.AppID] == 0 {
				delete(p.issueApps, entry.row.AppID)
			}
		}
		p.issueMu.Unlock()
		if err != nil && !retry {
			p.log.Warn("HTTP issue retry dropped", "app_id", entry.row.AppID, "err", err)
			p.ops.ObserveIssueEvent("retry_dropped")
		} else if err == nil && response.GetOutcome() == "retry_skipped" {
			p.ops.ObserveIssueEvent("retry_skipped")
		} else if err == nil {
			p.ops.ObserveIssueEvent("retry_recovered")
		}
	}
}
