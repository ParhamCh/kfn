package main

import (
	"context"
	"testing"
	"time"
)

func TestBurnDoesRealWork(t *testing.T) {
	p := cpuParams{dur: 100 * time.Millisecond, workers: 2, load: 1}
	res := runCPU(context.Background(), p)
	if res.Canceled {
		t.Fatalf("unexpected cancellation")
	}
	if res.TotalOps == 0 {
		t.Fatalf("total_ops = 0, expected real work")
	}
	if res.Workers != 2 {
		t.Fatalf("workers = %d, want 2", res.Workers)
	}
	if res.OpsPerSec == 0 {
		t.Fatalf("ops_per_sec = 0, expected a throughput estimate")
	}
}

func TestBurnRespectsDuration(t *testing.T) {
	p := cpuParams{dur: 150 * time.Millisecond, workers: 1, load: 1}
	start := time.Now()
	runCPU(context.Background(), p)
	if elapsed := time.Since(start); elapsed < 120*time.Millisecond || elapsed > 700*time.Millisecond {
		t.Fatalf("elapsed = %v, want ~150ms", elapsed)
	}
}

func TestCanceledContextReturnsPromptly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	p := cpuParams{dur: 10 * time.Second, workers: 2, load: 1}
	start := time.Now()
	res := runCPU(ctx, p)

	if !res.Canceled {
		t.Fatalf("expected Canceled=true")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("took %v, expected prompt return on cancellation", elapsed)
	}
}

func TestParamClamps(t *testing.T) {
	// duration above the ceiling clamps to maxDuration.
	if got := clampDuration(10*maxDuration, 0, maxDuration); got > maxDuration {
		t.Fatalf("duration clamp = %v, want <= %v", got, maxDuration)
	}
	// workers clamp into [1, maxWorkers].
	if got := clampInt(0, 1, maxWorkers); got != 1 {
		t.Fatalf("workers clamp(0) = %d, want 1", got)
	}
	if got := clampInt(maxWorkers+100, 1, maxWorkers); got != maxWorkers {
		t.Fatalf("workers clamp(high) = %d, want %d", got, maxWorkers)
	}
	// load clamps into [0,1].
	if got := clampFloat(2, 0, 1); got != 1 {
		t.Fatalf("load clamp(2) = %v, want 1", got)
	}
}

func TestDutyCycleReducesWork(t *testing.T) {
	// A 50% duty cycle should do clearly less work than a full burn over the same window.
	full := runCPU(context.Background(), cpuParams{dur: 300 * time.Millisecond, workers: 1, load: 1})
	half := runCPU(context.Background(), cpuParams{dur: 300 * time.Millisecond, workers: 1, load: 0.5})
	if half.TotalOps >= full.TotalOps {
		t.Fatalf("duty 0.5 did %d ops, full did %d — expected fewer", half.TotalOps, full.TotalOps)
	}
}
