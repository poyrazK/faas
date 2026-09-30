package main

import (
	"context"
	"sync"
	"time"
)

type testLoadArrivalEvidence struct {
	TargetRate           int     `json:"target_journeys_per_second"`
	Planned              int     `json:"planned"`
	Scheduled            int     `json:"scheduled"`
	Dropped              int     `json:"dropped"`
	DroppedCapacity      int     `json:"dropped_capacity"`
	DroppedLate          int     `json:"dropped_late"`
	SchedulingDurationMS float64 `json:"scheduling_duration_ms"`
	StartedPerSecond     float64 `json:"started_journeys_per_second"`
}

func (cfg *testLoadConfig) plannedArrivals() int {
	if cfg.Rate <= 0 || cfg.Duration <= 0 {
		return 0
	}
	// Arrivals occupy [0, duration), with the first at zero. Use integer
	// arithmetic so a fractional duration does not lose its final arrival.
	whole := int64(cfg.Duration/time.Second) * int64(cfg.Rate)
	fraction := int64(cfg.Duration%time.Second) * int64(cfg.Rate)
	return int(whole + (fraction+int64(time.Second)-1)/int64(time.Second))
}

type testLoadArrivalSchedule struct {
	rate     int
	duration time.Duration
	next     int
	total    int
}

// due returns at most one arrival. Missed deadlines are counted rather than
// replayed as a burst; no arrival starts once the scheduling window has ended.
func (s *testLoadArrivalSchedule) due(elapsed time.Duration) (arrival, dropped int, delay time.Duration, done bool) {
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed >= s.duration {
		dropped, s.next = s.total-s.next, s.total
		return 0, dropped, 0, true
	}
	if s.next == s.total {
		return 0, 0, s.duration - elapsed, false
	}
	deadline := (time.Duration(s.next)*time.Second + time.Duration(s.rate) - 1) / time.Duration(s.rate)
	if elapsed < deadline {
		return 0, 0, deadline - elapsed, false
	}
	latest := int(int64(elapsed) * int64(s.rate) / int64(time.Second))
	if latest >= s.total {
		latest = s.total - 1
	}
	dropped = latest - s.next
	s.next = latest + 1
	return latest + 1, dropped, 0, false
}

func (c *testLoadCollector) recordArrival(droppedLate int, admitted, droppedCapacity bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.arrivalsScheduled += droppedLate
	c.droppedLate += droppedLate
	if admitted || droppedCapacity {
		c.arrivalsScheduled++
	}
	if droppedCapacity {
		c.droppedCapacity++
	}
	if admitted {
		c.started++
		c.active++
		if c.active > c.peak {
			c.peak = c.active
		}
	}
}

func (c *testLoadCollector) arrivalBudgetExceeded() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.budgetExceeded
}

func runTestArrivalLoad(ctx context.Context, collector *testLoadCollector, cfg *testLoadConfig, started time.Time, runJourney func(int)) {
	schedule := testLoadArrivalSchedule{rate: cfg.Rate, duration: cfg.Duration, total: cfg.plannedArrivals()}
	slots := make(chan struct{}, cfg.maxVUs())
	var workers sync.WaitGroup
	for ctx.Err() == nil && !collector.arrivalBudgetExceeded() {
		arrival, late, delay, done := schedule.due(time.Since(started))
		collector.recordArrival(late, false, false)
		if done {
			break
		}
		if delay > 0 {
			if !waitTestLoad(ctx, delay) {
				break
			}
			continue
		}
		if ctx.Err() != nil {
			break
		}
		select {
		case slots <- struct{}{}:
			collector.recordArrival(0, true, false)
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer func() { <-slots }()
				runJourney(arrival)
			}()
		default:
			collector.recordArrival(0, false, true)
		}
	}
	collector.mu.Lock()
	collector.schedulingDuration = time.Since(started)
	collector.mu.Unlock()
	workers.Wait()
}
