package gateway

// ADR-831 amendment 8: a challenge rule with max_difficulty above its
// difficulty gets harder while it serves far more challenge pages than
// usual. Each gateway decides alone from the pages it served, minute by
// minute; a token carries the difficulty it was issued at, so verification
// never depends on this state.

import (
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	// challengeLoadMaxRules bounds the rules one gateway tracks. A rule
	// past the bound keeps its configured difficulty.
	challengeLoadMaxRules = 10000
	// challengeLoadIdleMinutes is how long a quiet rule keeps its learned
	// rate; after that it starts over, warm-up included.
	challengeLoadIdleMinutes = 120
)

// challengeLoads tracks, per challenge rule, the pages this gateway served
// and how far it has raised the rule's difficulty. The zero value is ready.
type challengeLoads struct {
	mu    sync.Mutex
	rules map[string]*challengeLoad
	swept int64 // the minute the map was last swept of idle rules
}

type challengeLoad struct {
	minute   int64   // the minute count belongs to (Unix minutes)
	count    int     // challenge pages served in that minute
	seen     int     // closed minutes the usual rate has learned from
	baseline float64 // usual pages per minute, learned while not raised
	level    int     // steps of EdgeChallengeRaiseStepBits above the difficulty
}

// issue counts one challenge page for rule at now. It returns the
// difficulty to issue the page at and how the level moved when this page
// closed earlier minutes: 1 raised, -1 lowered, 0 unchanged.
func (c *challengeLoads) issue(rule *EdgeRuleChallengeResolved, now time.Time) (difficulty, moved int) {
	step := api.EdgeChallengeRaiseStepBits
	maxLevel := (rule.MaxDifficulty - rule.Difficulty + step - 1) / step
	if maxLevel <= 0 {
		return rule.Difficulty, 0
	}
	minute := now.Unix() / 60
	c.mu.Lock()
	defer c.mu.Unlock()
	l := c.rules[rule.ID]
	if l == nil || minute-l.minute > challengeLoadIdleMinutes {
		if l == nil && len(c.rules) >= challengeLoadMaxRules && !c.sweep(minute) {
			return rule.Difficulty, 0
		}
		if c.rules == nil {
			c.rules = map[string]*challengeLoad{}
		}
		l = &challengeLoad{minute: minute}
		c.rules[rule.ID] = l
	}
	before := l.level
	for l.minute < minute {
		l.close(maxLevel)
	}
	l.count++
	return min(rule.Difficulty+l.level*step, rule.MaxDifficulty), sign(l.level - before)
}

// sweep drops rules idle past challengeLoadIdleMinutes, such as deleted
// ones, at most once a minute, and reports whether that made room.
func (c *challengeLoads) sweep(minute int64) bool {
	if c.swept != minute {
		c.swept = minute
		for id, l := range c.rules {
			if minute-l.minute > challengeLoadIdleMinutes {
				delete(c.rules, id)
			}
		}
	}
	return len(c.rules) < challengeLoadMaxRules
}

// close ends the current minute: a minute above the threshold raises a
// level and one below half of it lowers one. For its first
// EdgeChallengeBaselineMinutes minutes a rule has no usual rate yet, so the
// threshold is a fixed one and every minute teaches the usual rate; a rule
// created during an attack is raised at once and lowered after warm-up if
// that turns out to be its normal traffic. After warm-up only minutes at
// the lowest level teach it, so a long attack does not become the norm.
func (l *challengeLoad) close(maxLevel int) {
	n := float64(l.count)
	l.count = 0
	l.minute++
	warm := l.seen >= api.EdgeChallengeBaselineMinutes
	threshold := float64(api.EdgeChallengeRaiseFactor * api.EdgeChallengeRaiseFloorPerMinute)
	if warm {
		threshold = max(float64(api.EdgeChallengeRaiseFloorPerMinute), api.EdgeChallengeRaiseFactor*l.baseline)
	}
	switch {
	case n > threshold:
		l.level = min(l.level+1, maxLevel)
	case n < threshold/2:
		l.level = max(l.level-1, 0)
	}
	switch {
	case !warm:
		l.seen++
		l.baseline += (n - l.baseline) / float64(l.seen)
	case l.level == 0 && n <= threshold:
		l.baseline += (n - l.baseline) / api.EdgeChallengeBaselineMinutes
	}
}

func sign(n int) int {
	switch {
	case n > 0:
		return 1
	case n < 0:
		return -1
	}
	return 0
}
