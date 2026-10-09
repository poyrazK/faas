package gateway

// ADR-835: response-counted throttles check a bucket before forwarding and
// charge it only after the response status is known. These peeks never
// create, refill or consume a bucket; the charge goes through the ordinary
// Allow* paths so the local and central accounting stay one code path.

import "time"

// HasToken reports whether bucket id would admit a request now. A bucket
// never seen is full. In central mode this reads the last balance the
// central counter reported for this gateway's own charges.
func (l *Limiter) HasToken(id string, rps, burst float64) bool {
	if l == nil || l.noop {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.hasTokenLocked(id, rps, burst, l.now())
}

// HasConsumerToken is HasToken for a dimensional rule: the consumer's own
// bucket when tracked, a fresh bucket while the rule has room for a new
// consumer, and the shared __other__ bucket once it is at cap.
func (l *Limiter) HasConsumerToken(ruleKey, consumerID string, rps, burst float64, cap int) bool {
	if l == nil || l.noop {
		return true
	}
	if consumerID == ConsumerKeySentinel || cap <= 0 {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	consumers := l.ruleConsumers[ruleKey]
	key := ruleKey + "\x00" + consumerID
	if _, tracked := consumers[consumerID]; !tracked {
		if len(consumers) < cap {
			return true
		}
		key = ruleKey + "\x00" + ConsumerKeySentinel
	}
	return l.hasTokenLocked(key, rps, burst, l.now())
}

func (l *Limiter) hasTokenLocked(id string, rps, burst float64, now time.Time) bool {
	b := l.buckets[id]
	if b == nil {
		return burst >= 1
	}
	tokens := b.tokens + now.Sub(b.last).Seconds()*rps
	if tokens > burst {
		tokens = burst
	}
	return tokens >= 1
}
