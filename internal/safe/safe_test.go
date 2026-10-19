package safe

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestRecoverGoCall(t *testing.T) {
	func() {
		defer Recover("test")
		panic("boom")
	}()
	var wg sync.WaitGroup
	wg.Add(1)
	Go("goroutine", func() { defer wg.Done(); panic("in goroutine") })
	wg.Wait()
	if err := Call("op", func() error { panic("bad") }); err == nil || !strings.Contains(err.Error(), "internal error in op: bad") {
		t.Fatalf("Call = %v", err)
	}
	want := errors.New("plain")
	if err := Call("op", func() error { return want }); err != want {
		t.Fatalf("Call = %v", err)
	}
}
