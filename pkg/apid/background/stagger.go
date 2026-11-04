// Package background starts apid's periodic loops.
package background

import (
	"context"
	"math/rand/v2"
	"time"
)

// StartSpread is how far apart apid's loops start. apid starts about sixty
// loops in one instant, and loops with the same interval then tick together
// for the life of the process: an idle production control plane showed
// groups of four or five loops touching the database at the same moment and
// emptying the connection pool about 4,500 times an hour (Hunt 31). A random
// start within this window gives each loop its own phase.
const StartSpread = 10 * time.Second

// Staggered waits a random delay below spread, then runs run. It returns
// without running when ctx ends first.
func Staggered(ctx context.Context, spread time.Duration, run func()) {
	if spread > 0 {
		timer := time.NewTimer(rand.N(spread))
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
	run()
}
