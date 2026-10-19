package provider

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLimitsAndReport(t *testing.T) {
	l := Limits{}.WithDefaults()
	if l.Inbox != 300 || l.Other != 100 || l.Custom != 50 {
		t.Fatalf("%+v", l)
	}
	if l := (Limits{Inbox: 5, Other: 6, Custom: 7}).WithDefaults(); l.Inbox != 5 || l.Other != 6 || l.Custom != 7 {
		t.Fatalf("%+v", l)
	}
	SyncRequest{}.Report("x", 1, 2) // no Progress: no panic
	var got string
	SyncRequest{Progress: func(p string, d, n int) { got = p }}.Report("fetching", 1, 2)
	if got != "fetching" {
		t.Fatal(got)
	}
}

func TestChunk(t *testing.T) {
	c := Chunk([]int{1, 2, 3, 4, 5}, 2)
	if len(c) != 3 || len(c[2]) != 1 || c[2][0] != 5 {
		t.Fatalf("%v", c)
	}
	if Chunk([]int{}, 3) != nil {
		t.Fatal("empty chunk")
	}
}

func TestParallel(t *testing.T) {
	var max, cur, total atomic.Int32
	items := make([]int, 50)
	err := Parallel(context.Background(), 4, items, func(ctx context.Context, _ int) error {
		n := cur.Add(1)
		for {
			m := max.Load()
			if n <= m || max.CompareAndSwap(m, n) {
				break
			}
		}
		total.Add(1)
		cur.Add(-1)
		return nil
	})
	if err != nil || total.Load() != 50 || max.Load() > 4 {
		t.Fatalf("err %v total %d max %d", err, total.Load(), max.Load())
	}
	// The first error stops the rest and is returned.
	boom := errors.New("boom")
	var calls atomic.Int32
	err = Parallel(context.Background(), 0, items, func(ctx context.Context, i int) error {
		calls.Add(1)
		return boom
	})
	if err != boom || calls.Load() > 2 {
		t.Fatalf("err %v calls %d", err, calls.Load())
	}
	// Panics become errors.
	err = Parallel(context.Background(), 2, []int{1}, func(context.Context, int) error { panic("bad") })
	if err == nil || !strings.Contains(err.Error(), "internal error") {
		t.Fatalf("panic: %v", err)
	}
	// A cancelled parent context is reported.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Parallel(ctx, 2, items, func(context.Context, int) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
}
