package main

import "strings"

// driveInfo holds metadata about a Windows logical disk, used for snapshot
// comparison and device type classification.
type driveInfo struct {
	deviceID      string
	model         string
	interfaceType string
	driveType     uint32 // from Win32_LogicalDisk: 2=Removable, 3=Fixed
}

// classifyWinDevice maps a Windows logical disk to the (DeviceType, DeviceBus)
// pair used by the cross-platform model.
//
// Windows reports drive-letter-level objects rather than UDisks2's separate
// drive+partition split, so every event is modeled as a "block" volume.
// DeviceBus tracks the underlying interface — USB sticks become
// ("block", "usb"); fixed/removable disks of unknown interface become
// ("block", ""). Consumers filtering on DeviceType=="drive" will see no
// events on Windows; that asymmetry is documented in the manual cases.
func classifyWinDevice(info driveInfo) (deviceType, deviceBus string) {
	if strings.EqualFold(info.interfaceType, "USB") {
		return "block", "usb"
	}
	if info.driveType == 2 {
		// Removable media of unknown interface — best guess is still "usb"
		// because that's overwhelmingly the common case on consumer
		// hardware, but leave bus empty so filters can distinguish.
		return "block", ""
	}
	if info.driveType == 3 {
		return "block", ""
	}
	return "block", ""
}

// diffDrives compares two drive snapshots and returns added and removed events
// filtered by the deviceType and deviceBus axes (both AND-combined; "" or
// "all" means "no filter").
func diffDrives(previous, current map[string]driveInfo, deviceType, deviceBus string) (added, removed []DeviceEvent) {
	for letter, info := range current {
		if _, existed := previous[letter]; !existed {
			t, b := classifyWinDevice(info)
			event := DeviceEvent{
				DeviceName: info.model,
				DeviceType: t,
				DeviceBus:  b,
				DevicePath: info.deviceID,
				MountPoint: letter,
				Added:      true,
			}
			if matchesDeviceType(&event, deviceType) && matchesBus(&event, deviceBus) {
				added = append(added, event)
			}
		}
	}
	for letter, info := range previous {
		if _, exists := current[letter]; !exists {
			t, b := classifyWinDevice(info)
			event := DeviceEvent{
				DeviceName: info.model,
				DeviceType: t,
				DeviceBus:  b,
				DevicePath: info.deviceID,
				MountPoint: letter,
				Added:      false,
			}
			if matchesDeviceType(&event, deviceType) && matchesBus(&event, deviceBus) {
				removed = append(removed, event)
			}
		}
	}
	return
}
