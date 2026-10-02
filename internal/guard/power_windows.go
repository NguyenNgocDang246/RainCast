package guard

import (
	"syscall"
	"unsafe"
)

var (
	kernel32                    = syscall.NewLazyDLL("kernel32.dll")
	procGetSystemPowerStatus    = kernel32.NewProc("GetSystemPowerStatus")
	procSetThreadExecutionState = kernel32.NewProc("SetThreadExecutionState")
)

// preventSleep resets the system idle timer, as if the user had just been
// active, without keeping the display on. Called without ES_CONTINUOUS it is
// a one-off reset, so it needs no cleanup and does not depend on which OS
// thread the goroutine runs on.
func preventSleep() bool {
	const esSystemRequired = 0x00000001
	r, _, _ := procSetThreadExecutionState.Call(esSystemRequired)
	return r != 0
}

// systemPowerStatus mirrors SYSTEM_POWER_STATUS.
type systemPowerStatus struct {
	ACLineStatus        byte
	BatteryFlag         byte
	BatteryLifePercent  byte
	SystemStatusFlag    byte
	BatteryLifeTime     uint32
	BatteryFullLifeTime uint32
}

// onBattery reports whether a machine with a battery is unplugged.
func onBattery() bool {
	var s systemPowerStatus
	if r, _, _ := procGetSystemPowerStatus.Call(uintptr(unsafe.Pointer(&s))); r == 0 {
		return false
	}
	const noBattery = 128
	return s.ACLineStatus == 0 && s.BatteryFlag&noBattery == 0
}
