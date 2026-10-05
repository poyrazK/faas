package debugger

import (
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const debugCriticalPathMaxSpans = api.DebugCriticalPathMaxSpans

type debugCriticalTimedSpan struct {
	span  api.DebugTelemetrySpan
	start time.Time
	end   time.Time
}

func BuildCriticalPath(spans []api.DebugTelemetrySpan) *api.DebugRequestCriticalPath {
	timed := make([]debugCriticalTimedSpan, 0, len(spans))
	complete := true
	for _, span := range spans {
		start, startErr := time.Parse(time.RFC3339Nano, span.StartTime)
		end, endErr := time.Parse(time.RFC3339Nano, span.EndTime)
		if startErr != nil || endErr != nil || end.Before(start) {
			complete = false
			continue
		}
		timed = append(timed, debugCriticalTimedSpan{span: span, start: start, end: end})
	}
	if len(timed) == 0 {
		return nil
	}

	byID := make(map[string]int, len(timed))
	children := make(map[string][]int, len(timed))
	for index, span := range timed {
		byID[span.span.SpanID] = index
		if span.span.ParentSpanID != "" {
			children[span.span.ParentSpanID] = append(children[span.span.ParentSpanID], index)
		}
	}

	exclusive := make([]time.Duration, len(timed))
	for index, parent := range timed {
		intervals := make([][2]time.Time, 0, len(children[parent.span.SpanID]))
		for _, childIndex := range children[parent.span.SpanID] {
			child := timed[childIndex]
			start, end := child.start, child.end
			if start.Before(parent.start) {
				start = parent.start
			}
			if end.After(parent.end) {
				end = parent.end
			}
			if end.After(start) {
				intervals = append(intervals, [2]time.Time{start, end})
			}
		}
		sort.Slice(intervals, func(i, j int) bool {
			if !intervals[i][0].Equal(intervals[j][0]) {
				return intervals[i][0].Before(intervals[j][0])
			}
			return intervals[i][1].Before(intervals[j][1])
		})
		covered := time.Duration(0)
		if len(intervals) > 0 {
			currentStart, currentEnd := intervals[0][0], intervals[0][1]
			for _, interval := range intervals[1:] {
				if !interval[0].After(currentEnd) {
					if interval[1].After(currentEnd) {
						currentEnd = interval[1]
					}
					continue
				}
				covered += currentEnd.Sub(currentStart)
				currentStart, currentEnd = interval[0], interval[1]
			}
			covered += currentEnd.Sub(currentStart)
		}
		wall := parent.end.Sub(parent.start)
		exclusive[index] = wall - covered
		if exclusive[index] < 0 {
			exclusive[index] = 0
		}
	}

	var best []int
	var bestDuration time.Duration
	var bestEnd time.Time
	bestLeafID := ""
	for leafIndex, leaf := range timed {
		chain := make([]int, 0, debugCriticalPathMaxSpans)
		seen := make(map[string]struct{}, debugCriticalPathMaxSpans)
		current := leafIndex
		for len(chain) < debugCriticalPathMaxSpans {
			span := timed[current]
			if _, ok := seen[span.span.SpanID]; ok {
				complete = false
				break
			}
			seen[span.span.SpanID] = struct{}{}
			chain = append(chain, current)
			parentID := span.span.ParentSpanID
			if parentID == "" {
				break
			}
			parentIndex, ok := byID[parentID]
			if !ok {
				complete = false
				break
			}
			current = parentIndex
		}
		if len(chain) == debugCriticalPathMaxSpans && timed[chain[len(chain)-1]].span.ParentSpanID != "" {
			complete = false
		}
		if len(chain) == 0 {
			continue
		}
		root := timed[chain[len(chain)-1]]
		duration := leaf.end.Sub(root.start)
		if duration < 0 {
			duration = 0
		}
		if best == nil || duration > bestDuration || (duration == bestDuration && leaf.end.After(bestEnd)) || (duration == bestDuration && leaf.end.Equal(bestEnd) && leaf.span.SpanID < bestLeafID) {
			best = append(best[:0], chain...)
			bestDuration = duration
			bestEnd = leaf.end
			bestLeafID = leaf.span.SpanID
		}
	}
	if len(best) == 0 {
		return nil
	}

	// The parent walk above is leaf-to-root; expose the path in causal order.
	for left, right := 0, len(best)-1; left < right; left, right = left+1, right-1 {
		best[left], best[right] = best[right], best[left]
	}
	path := &api.DebugRequestCriticalPath{
		DurationMS: bestDuration.Milliseconds(),
		Complete:   complete && len(timed) == len(spans),
		SpanCount:  len(best),
		Spans:      make([]api.DebugCriticalPathSpan, 0, len(best)),
	}
	for _, index := range best {
		span := timed[index]
		exclusiveMS := exclusive[index].Milliseconds()
		path.Spans = append(path.Spans, api.DebugCriticalPathSpan{
			SpanID:         span.span.SpanID,
			ParentSpanID:   span.span.ParentSpanID,
			Name:           span.span.Name,
			Kind:           span.span.Kind,
			DependencyType: span.span.DependencyType,
			DependencyKind: span.span.DependencyKind,
			Status:         span.span.Status,
			StartTime:      span.span.StartTime,
			EndTime:        span.span.EndTime,
			DurationMS:     span.end.Sub(span.start).Milliseconds(),
			ExclusiveMS:    exclusiveMS,
		})
		if exclusiveMS > path.SlowestExclusiveMS {
			path.SlowestExclusiveMS = exclusiveMS
			path.SlowestSpanID = span.span.SpanID
			path.SlowestSpanName = span.span.Name
		}
	}
	return path
}
