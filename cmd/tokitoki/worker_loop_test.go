package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// The two halves must tick on their own schedules: a fast loop keeps running
// while a slow one is still working.
func TestRunIntervalLoopsAreIndependent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()

	var slow, fast int32
	done := make(chan struct{}, 2)

	go func() {
		runIntervalLoop(ctx, 10*time.Millisecond, func(context.Context) {
			atomic.AddInt32(&slow, 1)
			time.Sleep(250 * time.Millisecond) // a slow scan
		})
		done <- struct{}{}
	}()
	go func() {
		runIntervalLoop(ctx, 20*time.Millisecond, func(context.Context) {
			atomic.AddInt32(&fast, 1)
		})
		done <- struct{}{}
	}()

	<-done
	<-done

	s, f := atomic.LoadInt32(&slow), atomic.LoadInt32(&fast)
	t.Logf("slow ran %d times, fast ran %d times", s, f)
	if f <= s {
		t.Errorf("fast loop ran %d times, slow %d: the fast loop was blocked by the slow one", f, s)
	}
}

// A zero interval must not panic the worker.
func TestRunIntervalLoopRejectsZeroInterval(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	runs := 0
	runIntervalLoop(ctx, 0, func(context.Context) { runs++ })
	if runs == 0 {
		t.Fatal("work never ran")
	}
}
