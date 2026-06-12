package main

import (
	"context"
	"testing"
	"time"
)

func TestAllocatesAndHolds(t *testing.T) {
	p := ramParams{mb: 16, hold: 50 * time.Millisecond}
	res := runRAM(context.Background(), p)
	if res.Canceled {
		t.Fatalf("unexpected cancellation")
	}
	if res.AllocatedMB != 16 {
		t.Fatalf("allocated_mb = %d, want 16", res.AllocatedMB)
	}
	// RSS measurement is environment-sensitive; assert only that the peak didn't drop
	// below the baseline (the allocation should hold at least flat).
	if res.RSSPeakMB < res.RSSBeforeMB {
		t.Fatalf("rss_peak_mb %d < rss_before_mb %d", res.RSSPeakMB, res.RSSBeforeMB)
	}
}

func TestMBClampsToMax(t *testing.T) {
	if got := clampInt(maxMB+10_000, 0, maxMB); got != maxMB {
		t.Fatalf("mb clamp = %d, want %d", got, maxMB)
	}
}

func TestCanceledContextReturnsPromptly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	p := ramParams{mb: 8, hold: 10 * time.Second}
	start := time.Now()
	res := runRAM(ctx, p)

	if !res.Canceled {
		t.Fatalf("expected Canceled=true")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("took %v, expected prompt return on cancellation", elapsed)
	}
}

func TestRampStillAllocatesFullAmount(t *testing.T) {
	p := ramParams{mb: 8, ramp: 80 * time.Millisecond, hold: 0}
	res := runRAM(context.Background(), p)
	if res.Canceled {
		t.Fatalf("unexpected cancellation")
	}
	if res.AllocatedMB != 8 {
		t.Fatalf("allocated_mb = %d, want 8 after ramp", res.AllocatedMB)
	}
}
