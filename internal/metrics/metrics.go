// Package metrics records stage-level timing for the transcription pipeline.
// All functions are safe for concurrent use.
package metrics

import (
	"log/slog"
	"sync"
	"time"
)

var mu sync.Mutex
var stageTotals = map[string]time.Duration{}
var stageCounts = map[string]int{}
var counters = map[string]int64{}
var gauges = map[string]float64{}

// Record logs a timing measurement for the given stage and job.
// It also accumulates running totals for in-process metrics queries.
func Record(stage string, d time.Duration, jobID string) {
	slog.Info("stage timing",
		slog.String("stage", stage),
		slog.Int64("stage_ms", d.Milliseconds()),
		slog.String("job_id", jobID),
	)
	mu.Lock()
	stageTotals[stage] += d
	stageCounts[stage]++
	mu.Unlock()
}

// Summary returns a snapshot of accumulated stage totals.
// Keys are stage names; values are cumulative durations.
func Summary() map[string]time.Duration {
	mu.Lock()
	defer mu.Unlock()
	out := make(map[string]time.Duration, len(stageTotals))
	for k, v := range stageTotals {
		out[k] = v
	}
	return out
}

// Counts returns the number of times each stage has been recorded.
func Counts() map[string]int {
	mu.Lock()
	defer mu.Unlock()
	out := make(map[string]int, len(stageCounts))
	for k, v := range stageCounts {
		out[k] = v
	}
	return out
}

// AddCounter increments a named counter by delta.
func AddCounter(name string, delta int64) {
	mu.Lock()
	counters[name] += delta
	mu.Unlock()
}

// Counters returns a snapshot of named counters.
func Counters() map[string]int64 {
	mu.Lock()
	defer mu.Unlock()
	out := make(map[string]int64, len(counters))
	for k, v := range counters {
		out[k] = v
	}
	return out
}

// SetGauge sets a named gauge to a value.
func SetGauge(name string, value float64) {
	mu.Lock()
	gauges[name] = value
	mu.Unlock()
}

// AddGauge increments a named gauge by delta.
func AddGauge(name string, delta float64) {
	mu.Lock()
	gauges[name] += delta
	mu.Unlock()
}

// Gauges returns a snapshot of named gauges.
func Gauges() map[string]float64 {
	mu.Lock()
	defer mu.Unlock()
	out := make(map[string]float64, len(gauges))
	for k, v := range gauges {
		out[k] = v
	}
	return out
}
