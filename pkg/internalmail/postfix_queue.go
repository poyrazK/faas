package internalmail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type RelayQueues struct {
	Delivery StateStat
	Reports  StateStat
	Total    StateStat
}

// ReadPostfixQueues invokes only the local read-only inventory command, never
// a queue flush, hold, delete or resubmission. No raw output enters a metric or
// error. The caller must have checked the master and reports gates first.
func ReadPostfixQueues(ctx context.Context) (RelayQueues, error) {
	ctx, cancel := context.WithTimeout(ctx, api.InternalMailMonitoringTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/sbin/postqueue", "-j")
	command.WaitDelay = time.Second
	var output queueBuffer
	command.Stdout = &output
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return RelayQueues{}, errors.New("internal mail: Postfix queue inventory unavailable")
	}
	return parsePostfixQueues(bytes.NewReader(output.Bytes()), time.Now())
}

type queueBuffer struct{ bytes.Buffer }

func (b *queueBuffer) Write(p []byte) (int, error) {
	if len(p) > api.InternalMailMaxQueueInventoryBytes-b.Len() {
		return 0, ErrQuota
	}
	return b.Buffer.Write(p)
}

func parsePostfixQueues(input io.Reader, now time.Time) (RelayQueues, error) {
	var queues RelayQueues
	decoder := json.NewDecoder(input)
	for {
		var entry struct {
			Sender      *string `json:"sender"`
			ArrivalTime int64   `json:"arrival_time"`
			Recipients  []struct {
				Address string `json:"address"`
			} `json:"recipients"`
		}
		if err := decoder.Decode(&entry); errors.Is(err, io.EOF) {
			return queues, nil
		} else if err != nil {
			return RelayQueues{}, ErrInvalid
		}
		if entry.Sender == nil || entry.ArrivalTime <= 0 || entry.ArrivalTime > now.Add(api.InternalMailRelayClockSkew).Unix() || entry.Recipients == nil {
			return RelayQueues{}, ErrInvalid
		}
		queues.Total.Count++
		queues.Total.OldestSeconds = max(queues.Total.OldestSeconds, oldestRelayAge(entry.ArrivalTime, now))
		var target *StateStat
		if _, err := bounceRecipientID(*entry.Sender); err == nil {
			target = &queues.Delivery
		} else if *entry.Sender == "" || *entry.Sender == "MAILER-DAEMON" {
			// postqueue -j renders Postfix's null reverse path as the
			// MAILER-DAEMON sentinel, rather than an RFC envelope string.
			for _, recipient := range entry.Recipients {
				if _, err := bounceRecipientID(recipient.Address); err == nil {
					target = &queues.Reports
					break
				}
			}
		}
		if target != nil {
			target.Count++
			target.OldestSeconds = max(target.OldestSeconds, oldestRelayAge(entry.ArrivalTime, now))
		}
	}
}
