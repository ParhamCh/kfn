package main

import (
	"context"
	"math/rand/v2"
	"testing"
	"time"
)

func newRand() *rand.Rand { return rand.New(rand.NewPCG(1, 2)) }

func TestFixedSleepsAboutRequested(t *testing.T) {
	p := sleepParams{base: 40 * time.Millisecond, dist: distFixed}
	res := runSleep(context.Background(), p, newRand())
	if res.Canceled {
		t.Fatalf("unexpected cancellation")
	}
	if res.SleptMS < 35 || res.SleptMS > 250 {
		t.Fatalf("slept_ms = %d, want ~40ms", res.SleptMS)
	}
}

func TestPickClampsToMax(t *testing.T) {
	// A base far above the ceiling must still be clamped to maxDuration.
	p := sleepParams{base: 10 * maxDuration, dist: distFixed}
	if got := p.pick(newRand()); got > maxDuration {
		t.Fatalf("pick = %v, want <= %v", got, maxDuration)
	}
}

func TestExpStaysWithinCeiling(t *testing.T) {
	// The exponential tail is unbounded in theory; pick must always clamp it.
	p := sleepParams{base: 100 * time.Millisecond, dist: distExp}
	r := newRand()
	for range 1000 {
		if got := p.pick(r); got < 0 || got > maxDuration {
			t.Fatalf("pick = %v, out of [0, %v]", got, maxDuration)
		}
	}
}

func TestCanceledContextReturnsPromptly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	p := sleepParams{base: 10 * time.Second, dist: distFixed}
	start := time.Now()
	res := runSleep(ctx, p, newRand())

	if !res.Canceled {
		t.Fatalf("expected Canceled=true")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("took %v, expected to return promptly on cancellation", elapsed)
	}
}

func TestUniformRespectsJitterBand(t *testing.T) {
	base := 200 * time.Millisecond
	p := sleepParams{base: base, dist: distUniform, jitter: 0.5}
	r := newRand()
	lo := time.Duration(float64(base) * 0.5)
	hi := time.Duration(float64(base) * 1.5)
	for range 1000 {
		got := p.pick(r)
		if got < lo || got > hi {
			t.Fatalf("pick = %v, want within [%v, %v]", got, lo, hi)
		}
	}
}
