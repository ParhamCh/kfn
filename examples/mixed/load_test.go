package main

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/ParhamCh/kfn/pkg/runtime"
)

func TestRunsAllThreeConcurrently(t *testing.T) {
	p := mixedParams{
		cpu: 80 * time.Millisecond, workers: 2, load: 1,
		mb: 8, hold: 80 * time.Millisecond,
		sleep: 80 * time.Millisecond, dist: distFixed,
	}
	res := runMixed(context.Background(), p)

	if res.CPU == nil || res.CPU.TotalOps == 0 {
		t.Fatalf("expected cpu work, got %+v", res.CPU)
	}
	if res.RAM == nil || res.RAM.AllocatedMB != 8 {
		t.Fatalf("expected 8MB allocated, got %+v", res.RAM)
	}
	if res.Sleep == nil || res.Sleep.SleptMS < 60 {
		t.Fatalf("expected ~80ms sleep, got %+v", res.Sleep)
	}
	if res.Canceled {
		t.Fatalf("unexpected cancellation")
	}
	// Concurrent: total ≈ max(phases) ≈ 80ms, not the ~240ms sum.
	if res.TotalMS > 200 {
		t.Fatalf("total_ms = %d, expected concurrent (~80ms), not summed", res.TotalMS)
	}
}

func TestOnlyTriggeredLoadsRun(t *testing.T) {
	res := runMixed(context.Background(), mixedParams{sleep: 50 * time.Millisecond, dist: distFixed})
	if res.CPU != nil || res.RAM != nil {
		t.Fatalf("expected only sleep, got cpu=%v ram=%v", res.CPU, res.RAM)
	}
	if res.Sleep == nil {
		t.Fatalf("expected a sleep result")
	}
}

func TestCanceledReturnsPromptly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	res := runMixed(ctx, mixedParams{
		cpu: 10 * time.Second, workers: 1, load: 1,
		mb: 8, hold: 10 * time.Second,
		sleep: 10 * time.Second, dist: distFixed,
	})
	if !res.Canceled {
		t.Fatalf("expected canceled=true")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("took %v, expected prompt return on cancellation", elapsed)
	}
}

func TestQueryAndBodyAgree(t *testing.T) {
	q := url.Values{}
	q.Set("cpu", "1s")
	q.Set("mb", "16")
	q.Set("sleep", "200ms")
	pq, err := parseParams(&runtime.Request{Query: q})
	if err != nil {
		t.Fatalf("query parse: %v", err)
	}

	body := []byte(`{"cpu":"1s","mb":16,"sleep":"200ms"}`)
	pb, err := parseParams(&runtime.Request{Body: body, Query: url.Values{}})
	if err != nil {
		t.Fatalf("body parse: %v", err)
	}

	if pq.cpu != pb.cpu || pq.mb != pb.mb || pq.sleep != pb.sleep {
		t.Fatalf("query %+v != body %+v", pq, pb)
	}
}

func TestBadInputRejected(t *testing.T) {
	q := url.Values{}
	q.Set("cpu", "abc")
	if _, err := parseParams(&runtime.Request{Query: q}); err == nil {
		t.Fatalf("expected error for cpu=abc")
	}
	if _, err := parseParams(&runtime.Request{Body: []byte("{not json"), Query: url.Values{}}); err == nil {
		t.Fatalf("expected error for malformed JSON body")
	}
}

func TestClampAndDefaultHold(t *testing.T) {
	q := url.Values{}
	q.Set("mb", "99999") // above MIXED_MAX_MB
	q.Set("cpu", "3s")
	p, err := parseParams(&runtime.Request{Query: q})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.mb != maxMB {
		t.Fatalf("mb = %d, want clamped to %d", p.mb, maxMB)
	}
	// hold not given + mb set → defaults to span the request (≥ cpu).
	if p.hold < 3*time.Second {
		t.Fatalf("hold = %v, want ≥ cpu (3s) to span the request", p.hold)
	}
}
