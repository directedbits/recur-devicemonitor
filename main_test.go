//go:build linux || windows

package main

import (
	"strings"
	"testing"
)

func TestParseInput_DeviceConnected(t *testing.T) {
	jsonStr := `{"trigger_type":"DeviceConnected","options":{"device_type":"block","device_bus":"usb"},"config":{}}`
	parsed, err := parseInput(strings.NewReader(jsonStr))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.Input.TriggerType != "DeviceConnected" {
		t.Errorf("TriggerType = %q", parsed.Input.TriggerType)
	}
	if parsed.DeviceType != "block" {
		t.Errorf("DeviceType = %q", parsed.DeviceType)
	}
	if parsed.DeviceBus != "usb" {
		t.Errorf("DeviceBus = %q", parsed.DeviceBus)
	}
}

func TestParseInput_DeviceDisconnected(t *testing.T) {
	jsonStr := `{"trigger_type":"DeviceDisconnected","options":{},"config":{}}`
	parsed, err := parseInput(strings.NewReader(jsonStr))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.Input.TriggerType != "DeviceDisconnected" {
		t.Errorf("TriggerType = %q", parsed.Input.TriggerType)
	}
	if parsed.DeviceType != "all" {
		t.Errorf("DeviceType = %q, want all (default)", parsed.DeviceType)
	}
	if parsed.DeviceBus != "all" {
		t.Errorf("DeviceBus = %q, want all (default)", parsed.DeviceBus)
	}
}

func TestParseInput_DeviceBusOnly(t *testing.T) {
	jsonStr := `{"trigger_type":"DeviceConnected","options":{"device_bus":"loop"},"config":{}}`
	parsed, err := parseInput(strings.NewReader(jsonStr))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.DeviceType != "all" {
		t.Errorf("DeviceType = %q, want all (default)", parsed.DeviceType)
	}
	if parsed.DeviceBus != "loop" {
		t.Errorf("DeviceBus = %q, want loop", parsed.DeviceBus)
	}
}

func TestParseInput_InvalidTriggerType(t *testing.T) {
	jsonStr := `{"trigger_type":"BadType","options":{},"config":{}}`
	_, err := parseInput(strings.NewReader(jsonStr))
	if err == nil {
		t.Fatal("expected error for invalid trigger_type")
	}
}

func TestParseInput_InvalidJSON(t *testing.T) {
	_, err := parseInput(strings.NewReader("not json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
