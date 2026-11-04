package main

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Retain only Issues occurrences: retrying the legacy stream can double-count.
func (p *appErrorsPublisher) queueHTTPIssueRetries(batch []appErrorRow) {
	p.issueMu.Lock()
	defer p.issueMu.Unlock()
	now := time.Now()
	for _, row := range batch {
		if row.HTTPStatus < 500 || row.DeploymentID == "" || row.RequestID == "" || row.IssueEventID == "" {
			continue
		}
		if len(p.issueQueue) >= httpIssueRetryQueueLimit || p.issueApps[row.AppID] >= httpIssueRetryAppLimit {
			p.log.Warn("HTTP issue retry queue full", "app_id", row.AppID)
			p.ops.ObserveIssueEvent("retry_dropped")
			continue
		}
		p.issueQueue = append(p.issueQueue, httpIssueRetry{row: row, expires: now.Add(httpIssueRetryLifetime), next: now, delay: time.Second})
		p.issueApps[row.AppID]++
	}
}

// retryHTTPIssues gives the independent retry path a small bounded share of
// each publisher tick. Unimplemented is retried for rolling apid upgrades.
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
		if entry.expires.After(now) {
			callCtx, callCancel := context.WithTimeout(ctx, 2*time.Second)
			_, err = client.RecordHTTPIssue(callCtx, appErrorRequest(entry.row))
			callCancel()
		} else {
			err = context.DeadlineExceeded
		}
		code := status.Code(err)
		retry := err != nil && entry.expires.After(time.Now()) && code != codes.InvalidArgument && code != codes.NotFound && code != codes.PermissionDenied && code != codes.AlreadyExists
		p.issueMu.Lock()
		if retry {
			entry.next = time.Now().Add(entry.delay)
			entry.delay = min(time.Minute, entry.delay*2)
			p.issueQueue = append(p.issueQueue, entry)
		} else {
			p.issueApps[entry.row.AppID]--
			if p.issueApps[entry.row.AppID] == 0 {
				delete(p.issueApps, entry.row.AppID)
			}
		}
		p.issueMu.Unlock()
		if err != nil && !retry {
			p.log.Warn("HTTP issue retry dropped", "app_id", entry.row.AppID, "err", err)
			p.ops.ObserveIssueEvent("retry_dropped")
		} else if err == nil {
			p.ops.ObserveIssueEvent("retry_recovered")
		}
	}
}
