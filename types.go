package main

import "strings"

// DeviceEvent represents a parsed device event.
//
// DeviceType and DeviceBus are orthogonal axes: DeviceType describes the
// kind of UDisks2 object ("drive" or "block"), while DeviceBus describes
// the connection medium ("usb", "loop", "sata", "ata", "nvme", ...).
// A USB stick fires one drive event plus one block event per partition;
// all share DeviceBus="usb", and the block events carry DrivePath
// pointing at the drive so consumers can correlate them.
type DeviceEvent struct {
	DeviceName string
	DeviceType string // "drive" or "block"
	DeviceBus  string // "usb", "loop", "sata", "ata", "nvme", ... or "" when unknown
	DevicePath string
	DrivePath  string // parent drive D-Bus object path for block events; empty for drive events
	MountPoint string
	Added      bool // true = connected, false = disconnected
}

// matchesDeviceType checks whether a DeviceEvent passes the device type filter.
func matchesDeviceType(event *DeviceEvent, filter string) bool {
	if filter == "" || filter == "all" {
		return true
	}
	return strings.EqualFold(event.DeviceType, filter)
}

// matchesBus checks whether a DeviceEvent passes the connection bus filter.
// Events with an empty DeviceBus never match a specific bus filter — they
// only match "all".
func matchesBus(event *DeviceEvent, filter string) bool {
	if filter == "" || filter == "all" {
		return true
	}
	return strings.EqualFold(event.DeviceBus, filter)
}

// DeviceSubscriber abstracts platform-specific device event subscription.
type DeviceSubscriber interface {
	// Subscribe starts listening for device events filtered by deviceType
	// and deviceBus (both AND-combined; "" or "all" means "no filter").
	// Returns a channel of DeviceEvents. The channel is closed when monitoring stops.
	Subscribe(deviceType, deviceBus string) (<-chan DeviceEvent, error)
	// Close stops the subscriber and releases resources.
	Close()
}
