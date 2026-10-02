//go:build !windows

package guard

// onBattery is only implemented on Windows; servers have no battery.
func onBattery() bool { return false }

// preventSleep is only implemented on Windows; servers do not sleep.
func preventSleep() bool { return false }
