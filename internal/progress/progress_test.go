package progress

import (
	"testing"
	"time"
)

func TestEstimate(t *testing.T) {
	if e := New(25, 100, time.Minute); e.Left != 3*time.Minute {
		t.Fatalf("left %v, want 3m", e.Left)
	}
	if e := New(0, 100, time.Minute); e.Left >= 0 {
		t.Fatal("no pace yet: left unknown")
	}
	if e := New(100, 100, time.Minute); e.Left != 0 {
		t.Fatalf("done: left %v", e.Left)
	}
	args := New(1, 4, time.Second).Args()
	if len(args) != 10 || args[1] != "1/4" || args[3] != "25.0%" {
		t.Fatalf("args %v", args)
	}
}
