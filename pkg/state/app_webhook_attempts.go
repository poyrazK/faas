package state

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var ErrInvalidAppWebhookAttemptPageToken = errors.New("state: invalid webhook attempt page token")

func appWebhookAttemptTimes(meta []AppWebhookAttemptMetadata) (time.Time, time.Time, int) {
	finished := time.Now().UTC()
	started := finished
	code := 0
	if len(meta) > 0 {
		if !meta[0].FinishedAt.IsZero() {
			finished = meta[0].FinishedAt
		}
		if !meta[0].StartedAt.IsZero() {
			started = meta[0].StartedAt
		} else {
			started = finished
		}
		code = meta[0].ResponseCode
	}
	if finished.Before(started) {
		finished = started
	}
	return started, finished, code
}

func encodeAppWebhookAttemptPageToken(a AppWebhookDeliveryAttempt) string {
	return fmt.Sprintf("%d:%d", a.ReplayGeneration, a.AttemptNumber)
}

func decodeAppWebhookAttemptPageToken(token string) (int, int, bool) {
	generationText, numberText, ok := strings.Cut(token, ":")
	if !ok {
		return 0, 0, false
	}
	generation, err := strconv.Atoi(generationText)
	if err != nil || generation < 0 || generation > (1<<31)-1 {
		return 0, 0, false
	}
	number, err := strconv.Atoi(numberText)
	if err != nil || number < 1 || number > 8 {
		return 0, 0, false
	}
	return generation, number, true
}
