package gateway

// adr: 831 — amendment 8: a challenge rule with max_difficulty above its
// difficulty is raised two bits per minute while one gateway serves far
// more challenge pages than usual, lowered as the load eases, and a long
// attack does not become its usual rate.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var loadEpoch = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

// loadRun serves pages[i] challenge pages in minute i, starting at minute
// start, and returns the difficulty of each minute's first page and how the
// level moved when it was served.
func loadRun(c *challengeLoads, rule *EdgeRuleChallengeResolved, start int, pages ...int) (difficulty, moved []int) {
	for i, n := range pages {
		at := loadEpoch.Add(time.Duration(start+i) * time.Minute)
		for p := 0; p < n; p++ {
			d, m := c.issue(rule, at.Add(time.Duration(p)*time.Millisecond))
			if p == 0 {
				difficulty, moved = append(difficulty, d), append(moved, m)
			}
		}
	}
	return difficulty, moved
}

func repeat(n, times int) []int {
	out := make([]int, times)
	for i := range out {
		out[i] = n
	}
	return out
}

func TestChallengeLoadFixedDifficulty(t *testing.T) {
	var c challengeLoads
	rule := &EdgeRuleChallengeResolved{ID: "fixed", Difficulty: 16, MaxDifficulty: 16}
	got, _ := loadRun(&c, rule, 0, 1000, 1000, 1000)
	for _, d := range got {
		if d != 16 {
			t.Fatalf("difficulty = %v, want 16 throughout", got)
		}
	}
	if len(c.rules) != 0 {
		t.Errorf("a fixed rule is tracked: %v", c.rules)
	}
}

func TestChallengeLoadRaisesAndLowers(t *testing.T) {
	var c challengeLoads
	rule := &EdgeRuleChallengeResolved{ID: "r", Difficulty: 16, MaxDifficulty: 21}
	usual := api.EdgeChallengeBaselineMinutes
	// Warm-up at 20 pages a minute, below the warm-up threshold.
	got, _ := loadRun(&c, rule, 0, repeat(20, usual)...)
	for _, d := range got {
		if d != 16 {
			t.Fatalf("raised during a quiet warm-up: %v", got)
		}
	}
	// The usual rate is 20, so the threshold is the 60-page floor. Four
	// minutes of 200 raise two bits a minute up to the cap, then four quiet
	// minutes lower it again.
	got, moved := loadRun(&c, rule, usual, 200, 200, 200, 200, 5, 5, 5, 5, 5)
	if want := []int{16, 18, 20, 21, 21, 20, 18, 16, 16}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("difficulty by minute = %v, want %v", got, want)
	}
	if want := []int{0, 1, 1, 1, 0, -1, -1, -1, 0}; fmt.Sprint(moved) != fmt.Sprint(want) {
		t.Errorf("moves by minute = %v, want %v", moved, want)
	}
}

func TestChallengeLoadHoldsBetweenThresholds(t *testing.T) {
	var c challengeLoads
	rule := &EdgeRuleChallengeResolved{ID: "r", Difficulty: 16, MaxDifficulty: 22}
	usual := api.EdgeChallengeBaselineMinutes
	loadRun(&c, rule, 0, repeat(50, usual)...)
	// Threshold 200: 201 raises, 150 (above half) holds, 99 (below half)
	// lowers.
	got, _ := loadRun(&c, rule, usual, 201, 150, 150, 99, 99)
	if want := []int{16, 18, 18, 18, 16}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("difficulty by minute = %v, want %v", got, want)
	}
}

func TestChallengeLoadLongAttackIsNotTheNorm(t *testing.T) {
	var c challengeLoads
	rule := &EdgeRuleChallengeResolved{ID: "r", Difficulty: 16, MaxDifficulty: 22}
	usual := api.EdgeChallengeBaselineMinutes
	loadRun(&c, rule, 0, repeat(50, usual)...)
	got, _ := loadRun(&c, rule, usual, repeat(1000, 60)...)
	if last := got[len(got)-1]; last != 22 {
		t.Fatalf("difficulty after an hour of attack = %d, want 22", last)
	}
	if b := c.rules["r"].baseline; b > 51 {
		t.Errorf("usual rate learned from the attack: %.1f pages a minute", b)
	}
}

func TestChallengeLoadNewRuleUnderAttack(t *testing.T) {
	var c challengeLoads
	rule := &EdgeRuleChallengeResolved{ID: "r", Difficulty: 16, MaxDifficulty: 20}
	floor := api.EdgeChallengeRaiseFactor * api.EdgeChallengeRaiseFloorPerMinute
	// A rule created mid-attack is raised during warm-up...
	got, _ := loadRun(&c, rule, 0, floor+1, floor+1, floor+1)
	if want := []int{16, 18, 20}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("warm-up difficulty = %v, want %v", got, want)
	}
	// ...and if that rate is its normal traffic, lowered after warm-up.
	got, _ = loadRun(&c, rule, 3, repeat(floor+1, api.EdgeChallengeBaselineMinutes+2)...)
	if last := got[len(got)-1]; last != 16 {
		t.Errorf("difficulty after learning a busy rule's usual rate = %d, want 16", last)
	}
}

func TestChallengeLoadIdleRestarts(t *testing.T) {
	var c challengeLoads
	rule := &EdgeRuleChallengeResolved{ID: "r", Difficulty: 16, MaxDifficulty: 22}
	loadRun(&c, rule, 0, repeat(30, api.EdgeChallengeBaselineMinutes)...)
	loadRun(&c, rule, api.EdgeChallengeBaselineMinutes+challengeLoadIdleMinutes+1, 1)
	if l := c.rules["r"]; l.seen != 0 || l.baseline != 0 {
		t.Errorf("a rule idle past %d minutes kept its history: %+v", challengeLoadIdleMinutes, *l)
	}
}

func TestChallengeLoadBoundsTrackedRules(t *testing.T) {
	c := challengeLoads{rules: map[string]*challengeLoad{}}
	for i := 0; i < challengeLoadMaxRules; i++ {
		c.rules[strconv.Itoa(i)] = &challengeLoad{minute: loadEpoch.Unix() / 60}
	}
	rule := &EdgeRuleChallengeResolved{ID: "one-too-many", Difficulty: 14, MaxDifficulty: 22}
	if d, _ := c.issue(rule, loadEpoch); d != 14 {
		t.Errorf("difficulty past the bound = %d, want the rule's own 14", d)
	}
	if len(c.rules) != challengeLoadMaxRules {
		t.Errorf("tracked %d rules, want at most %d", len(c.rules), challengeLoadMaxRules)
	}
	// Once the others have been idle past the limit (deleted rules, say),
	// a sweep makes room and the new rule is tracked.
	later := loadEpoch.Add((challengeLoadIdleMinutes + 1) * time.Minute)
	c.issue(rule, later)
	if _, ok := c.rules[rule.ID]; !ok || len(c.rules) != 1 {
		t.Errorf("after the others went idle: tracked %d rules, new rule tracked %v", len(c.rules), ok)
	}
}

// TestChallengeRaisedPageRoundTrip: a page served while the rule is raised
// carries the raised difficulty in its token, and work at that difficulty
// earns a pass.
func TestChallengeRaisedPageRoundTrip(t *testing.T) {
	rule := &EdgeRuleChallengeResolved{ID: "rule-1", AppID: "app-1", Difficulty: 10, MaxDifficulty: 14, PassTTLSeconds: 3600}
	now := loadEpoch.Add(time.Hour)
	h := challengeTestHandler(rule, now)
	h.challengeLoad.rules = map[string]*challengeLoad{
		rule.ID: {minute: now.Unix() / 60, seen: api.EdgeChallengeBaselineMinutes, baseline: 10, level: 1},
	}
	w := httptest.NewRecorder()
	if !h.applyEdgeRuleChallenge(w, browserRequest(http.MethodGet, "/shop"), App{ID: "app-1"}, false) {
		t.Fatal("challenge not served")
	}
	if !strings.Contains(w.Body.String(), `data-difficulty="12"`) {
		t.Fatalf("page not served at the raised difficulty 12:\n%s", w.Body.String())
	}
	token := tokenAttr.FindStringSubmatch(w.Body.String())[1]
	form := url.Values{"token": {token}, "counter": {solveCounter(t, token, 12)}, "return": {"/shop"}}
	sw := httptest.NewRecorder()
	req := browserRequestBody(http.MethodPost, ChallengeSolvePath, form.Encode())
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if !h.handleChallengeSolve(sw, req, App{ID: "app-1"}) || sw.Code != http.StatusSeeOther {
		t.Fatalf("solve status = %d, want 303; body %s", sw.Code, sw.Body.String())
	}
}
