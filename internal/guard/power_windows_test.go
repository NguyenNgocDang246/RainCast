package guard

import "testing"

// The call must succeed on a real Windows machine.
func TestPreventSleepWorksOnWindows(t *testing.T) {
	if !preventSleep() {
		t.Fatal("SetThreadExecutionState failed")
	}
}
