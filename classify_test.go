//go:build linux || windows

package main

import "testing"

func TestClassifyWinDevice(t *testing.T) {
	tests := []struct {
		name     string
		info     driveInfo
		wantType string
		wantBus  string
	}{
		{"USB interface", driveInfo{interfaceType: "USB"}, "block", "usb"},
		{"usb lowercase", driveInfo{interfaceType: "usb"}, "block", "usb"},
		{"removable drive type 2 (no interface)", driveInfo{driveType: 2}, "block", ""},
		{"fixed drive type 3", driveInfo{driveType: 3}, "block", ""},
		{"unknown type", driveInfo{driveType: 5}, "block", ""},
		{"USB takes priority over type 3", driveInfo{interfaceType: "USB", driveType: 3}, "block", "usb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotType, gotBus := classifyWinDevice(tt.info)
			if gotType != tt.wantType || gotBus != tt.wantBus {
				t.Errorf("classifyWinDevice() = (%q, %q), want (%q, %q)", gotType, gotBus, tt.wantType, tt.wantBus)
			}
		})
	}
}

func TestDiffDrives_NewDrive(t *testing.T) {
	prev := map[string]driveInfo{}
	curr := map[string]driveInfo{
		"D:": {deviceID: "D:", model: "USB Drive", interfaceType: "USB", driveType: 2},
	}
	added, removed := diffDrives(prev, curr, "all", "all")
	if len(added) != 1 {
		t.Fatalf("expected 1 added, got %d", len(added))
	}
	if !added[0].Added {
		t.Error("expected Added = true")
	}
	if added[0].DeviceType != "block" || added[0].DeviceBus != "usb" {
		t.Errorf("type/bus = (%q, %q), want (block, usb)", added[0].DeviceType, added[0].DeviceBus)
	}
	if added[0].MountPoint != "D:" {
		t.Errorf("MountPoint = %q, want D:", added[0].MountPoint)
	}
	if len(removed) != 0 {
		t.Errorf("expected 0 removed, got %d", len(removed))
	}
}

func TestDiffDrives_RemovedDrive(t *testing.T) {
	prev := map[string]driveInfo{
		"E:": {deviceID: "E:", model: "External", interfaceType: "USB"},
	}
	curr := map[string]driveInfo{}
	added, removed := diffDrives(prev, curr, "all", "all")
	if len(removed) != 1 {
		t.Fatalf("expected 1 removed, got %d", len(removed))
	}
	if removed[0].Added {
		t.Error("expected Added = false")
	}
	if len(added) != 0 {
		t.Errorf("expected 0 added, got %d", len(added))
	}
}

func TestDiffDrives_NoDiff(t *testing.T) {
	drives := map[string]driveInfo{
		"C:": {deviceID: "C:", model: "System", driveType: 3},
	}
	added, removed := diffDrives(drives, drives, "all", "all")
	if len(added) != 0 || len(removed) != 0 {
		t.Errorf("expected no diff, got %d added %d removed", len(added), len(removed))
	}
}

func TestDiffDrives_FilterByBus(t *testing.T) {
	prev := map[string]driveInfo{}
	curr := map[string]driveInfo{
		"D:": {deviceID: "D:", model: "USB Drive", interfaceType: "USB", driveType: 2},
		"E:": {deviceID: "E:", model: "Fixed Disk", driveType: 3},
	}
	added, _ := diffDrives(prev, curr, "all", "usb")
	if len(added) != 1 {
		t.Fatalf("expected 1 USB added, got %d", len(added))
	}
	if added[0].DeviceBus != "usb" {
		t.Errorf("DeviceBus = %q, want usb", added[0].DeviceBus)
	}
}

func TestDiffDrives_FilterByType(t *testing.T) {
	prev := map[string]driveInfo{}
	curr := map[string]driveInfo{
		"D:": {deviceID: "D:", model: "USB Drive", interfaceType: "USB", driveType: 2},
	}
	// Windows classifies everything as "block" so a drive filter excludes everything.
	added, _ := diffDrives(prev, curr, "drive", "all")
	if len(added) != 0 {
		t.Errorf("expected 0 added (Windows has no drive events), got %d", len(added))
	}
}

func TestDiffDrives_SwapDrives(t *testing.T) {
	prev := map[string]driveInfo{
		"D:": {deviceID: "D:", model: "Old USB", interfaceType: "USB"},
	}
	curr := map[string]driveInfo{
		"E:": {deviceID: "E:", model: "New USB", interfaceType: "USB"},
	}
	added, removed := diffDrives(prev, curr, "all", "all")
	if len(added) != 1 || len(removed) != 1 {
		t.Fatalf("expected 1 added 1 removed, got %d added %d removed", len(added), len(removed))
	}
}

func TestEventVerb(t *testing.T) {
	tests := []struct {
		added bool
		want  string
	}{
		{true, "connected"},
		{false, "disconnected"},
	}
	for _, tt := range tests {
		got := eventVerb(tt.added)
		if got != tt.want {
			t.Errorf("eventVerb(%v) = %q, want %q", tt.added, got, tt.want)
		}
	}
}
