//go:build linux

package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// mockDBusConn implements DBusConn for testing.
type mockDBusConn struct {
	addMatchErr error
	signals     chan *dbus.Signal
}

func newMockConn() *mockDBusConn {
	return &mockDBusConn{
		signals: make(chan *dbus.Signal, 16),
	}
}

func (c *mockDBusConn) AddMatchSignal(options ...dbus.MatchOption) error {
	return c.addMatchErr
}

func (c *mockDBusConn) Signal(ch chan<- *dbus.Signal) {
	go func() {
		for sig := range c.signals {
			ch <- sig
		}
		close(ch)
	}()
}

// Object returns nil; tests that need to exercise the GetProperty fallback
// should drive the in-memory busCache directly (or via a prior
// InterfacesAdded signal for the parent drive). The mock can't synthesize
// a working dbus.BusObject anyway.
func (c *mockDBusConn) Object(dest string, path dbus.ObjectPath) dbus.BusObject {
	return nil
}

func TestSubscribe_BlockDevice(t *testing.T) {
	conn := newMockConn()
	events, err := Subscribe(conn, "all", "all")
	if err != nil {
		t.Fatalf("Subscribe error: %v", err)
	}

	conn.signals <- &dbus.Signal{
		Name: interfacesAdded,
		Body: []interface{}{
			dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/sdb1"),
			map[string]map[string]dbus.Variant{
				blockIf: {},
			},
		},
	}

	select {
	case evt := <-events:
		if evt.DeviceName != "sdb1" {
			t.Errorf("DeviceName = %q, want sdb1", evt.DeviceName)
		}
		if !evt.Added {
			t.Error("expected Added = true")
		}
		if evt.DeviceType != "block" {
			t.Errorf("DeviceType = %q, want block", evt.DeviceType)
		}
		if evt.DeviceBus != "" {
			t.Errorf("DeviceBus = %q, want empty (no parent drive, no Loop iface)", evt.DeviceBus)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}

	close(conn.signals)
}

func TestSubscribe_FilterByBus(t *testing.T) {
	conn := newMockConn()
	events, err := Subscribe(conn, "all", "usb")
	if err != nil {
		t.Fatalf("Subscribe error: %v", err)
	}

	// Non-USB drive: should be filtered out.
	conn.signals <- &dbus.Signal{
		Name: interfacesAdded,
		Body: []interface{}{
			dbus.ObjectPath("/org/freedesktop/UDisks2/drives/sata1"),
			map[string]map[string]dbus.Variant{
				driveIf: {"ConnectionBus": dbus.MakeVariant("sata")},
			},
		},
	}

	// USB drive: should pass.
	conn.signals <- &dbus.Signal{
		Name: interfacesAdded,
		Body: []interface{}{
			dbus.ObjectPath("/org/freedesktop/UDisks2/drives/usb1"),
			map[string]map[string]dbus.Variant{
				driveIf: {"ConnectionBus": dbus.MakeVariant("usb")},
			},
		},
	}

	select {
	case evt := <-events:
		if evt.DeviceBus != "usb" {
			t.Errorf("expected USB event, got bus=%q", evt.DeviceBus)
		}
		if evt.DeviceType != "drive" {
			t.Errorf("DeviceType = %q, want drive", evt.DeviceType)
		}
		if evt.DeviceName != "usb1" {
			t.Errorf("DeviceName = %q, want usb1", evt.DeviceName)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out -- USB event was filtered when it shouldn't have been")
	}

	close(conn.signals)
}

func TestSubscribe_FilterByType(t *testing.T) {
	conn := newMockConn()
	events, err := Subscribe(conn, "block", "all")
	if err != nil {
		t.Fatalf("Subscribe error: %v", err)
	}

	// Drive event: should be filtered out by type=block.
	conn.signals <- &dbus.Signal{
		Name: interfacesAdded,
		Body: []interface{}{
			dbus.ObjectPath("/org/freedesktop/UDisks2/drives/usb1"),
			map[string]map[string]dbus.Variant{
				driveIf: {"ConnectionBus": dbus.MakeVariant("usb")},
			},
		},
	}

	// Block event: should pass.
	conn.signals <- &dbus.Signal{
		Name: interfacesAdded,
		Body: []interface{}{
			dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/sda1"),
			map[string]map[string]dbus.Variant{blockIf: {}},
		},
	}

	select {
	case evt := <-events:
		if evt.DeviceType != "block" {
			t.Errorf("DeviceType = %q, want block", evt.DeviceType)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for block event")
	}

	close(conn.signals)
}

func TestSubscribe_RemovedDevice(t *testing.T) {
	conn := newMockConn()
	events, err := Subscribe(conn, "all", "all")
	if err != nil {
		t.Fatalf("Subscribe error: %v", err)
	}

	conn.signals <- &dbus.Signal{
		Name: interfacesRemoved,
		Body: []interface{}{
			dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/sdb1"),
			[]string{blockIf},
		},
	}

	select {
	case evt := <-events:
		if evt.Added {
			t.Error("expected Added = false")
		}
		if evt.DeviceName != "sdb1" {
			t.Errorf("DeviceName = %q", evt.DeviceName)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}

	close(conn.signals)
}

func TestSubscribe_UnrelatedSignalIgnored(t *testing.T) {
	conn := newMockConn()
	events, err := Subscribe(conn, "all", "all")
	if err != nil {
		t.Fatalf("Subscribe error: %v", err)
	}

	conn.signals <- &dbus.Signal{
		Name: "org.freedesktop.DBus.Properties.PropertiesChanged",
		Body: []interface{}{},
	}

	conn.signals <- &dbus.Signal{
		Name: interfacesAdded,
		Body: []interface{}{
			dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/sdc1"),
			map[string]map[string]dbus.Variant{blockIf: {}},
		},
	}

	select {
	case evt := <-events:
		if evt.DeviceName != "sdc1" {
			t.Errorf("DeviceName = %q, want sdc1", evt.DeviceName)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}

	close(conn.signals)
}

func TestSubscribe_AddMatchError(t *testing.T) {
	conn := newMockConn()
	conn.addMatchErr = fmt.Errorf("permission denied")

	_, err := Subscribe(conn, "all", "all")
	if err == nil {
		t.Fatal("expected error when AddMatchSignal fails")
	}
}

func TestSubscribe_ChannelCloses(t *testing.T) {
	conn := newMockConn()
	events, err := Subscribe(conn, "all", "all")
	if err != nil {
		t.Fatalf("Subscribe error: %v", err)
	}

	close(conn.signals)

	select {
	case _, ok := <-events:
		if ok {
			for range events {
			}
		}
	case <-time.After(time.Second):
		t.Fatal("events channel not closed after signal source closed")
	}
}

func TestSubscribe_BlockInheritsBusFromCachedDrive(t *testing.T) {
	// Drive add caches bus=usb. Subsequent partition add (no ConnectionBus
	// in its own ifaces map) should look up the parent drive in the cache
	// and report DeviceBus=usb without needing a D-Bus round-trip.
	conn := newMockConn()
	events, err := Subscribe(conn, "all", "all")
	if err != nil {
		t.Fatalf("Subscribe error: %v", err)
	}

	conn.signals <- &dbus.Signal{
		Name: interfacesAdded,
		Body: []interface{}{
			dbus.ObjectPath("/org/freedesktop/UDisks2/drives/USB1"),
			map[string]map[string]dbus.Variant{
				driveIf: {"ConnectionBus": dbus.MakeVariant("usb")},
			},
		},
	}
	conn.signals <- &dbus.Signal{
		Name: interfacesAdded,
		Body: []interface{}{
			dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/sda1"),
			map[string]map[string]dbus.Variant{
				blockIf: {
					"Drive": dbus.MakeVariant(dbus.ObjectPath("/org/freedesktop/UDisks2/drives/USB1")),
				},
			},
		},
	}

	driveEvt := <-events
	if driveEvt.DeviceType != "drive" || driveEvt.DeviceBus != "usb" {
		t.Fatalf("drive event = (%q, %q), want (drive, usb)", driveEvt.DeviceType, driveEvt.DeviceBus)
	}
	blockEvt := <-events
	if blockEvt.DeviceType != "block" || blockEvt.DeviceBus != "usb" {
		t.Errorf("block event = (%q, %q), want (block, usb)", blockEvt.DeviceType, blockEvt.DeviceBus)
	}
	if blockEvt.DrivePath != "/org/freedesktop/UDisks2/drives/USB1" {
		t.Errorf("DrivePath = %q", blockEvt.DrivePath)
	}

	close(conn.signals)
}

func TestSubscribe_RemovedBlockReplaysBusAndDrivePath(t *testing.T) {
	// After a partition's InterfacesAdded sets DeviceBus and DrivePath, its
	// InterfacesRemoved should replay both — InterfacesRemoved payloads
	// carry no properties, so without the cache the remove event would have
	// empty bus/drive fields.
	conn := newMockConn()
	events, err := Subscribe(conn, "all", "all")
	if err != nil {
		t.Fatalf("Subscribe error: %v", err)
	}

	conn.signals <- &dbus.Signal{
		Name: interfacesAdded,
		Body: []interface{}{
			dbus.ObjectPath("/org/freedesktop/UDisks2/drives/USB1"),
			map[string]map[string]dbus.Variant{
				driveIf: {"ConnectionBus": dbus.MakeVariant("usb")},
			},
		},
	}
	conn.signals <- &dbus.Signal{
		Name: interfacesAdded,
		Body: []interface{}{
			dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/sda1"),
			map[string]map[string]dbus.Variant{
				blockIf: {
					"Drive": dbus.MakeVariant(dbus.ObjectPath("/org/freedesktop/UDisks2/drives/USB1")),
				},
			},
		},
	}
	conn.signals <- &dbus.Signal{
		Name: interfacesRemoved,
		Body: []interface{}{
			dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/sda1"),
			[]string{blockIf},
		},
	}

	<-events // drive add
	<-events // block add
	removeEvt := <-events
	if removeEvt.Added {
		t.Fatal("expected Added=false")
	}
	if removeEvt.DeviceBus != "usb" {
		t.Errorf("DeviceBus = %q, want usb (from cache)", removeEvt.DeviceBus)
	}
	if removeEvt.DrivePath != "/org/freedesktop/UDisks2/drives/USB1" {
		t.Errorf("DrivePath = %q (from cache)", removeEvt.DrivePath)
	}

	close(conn.signals)
}

func TestParseInterfacesAdded_BlockDevice(t *testing.T) {
	sig := &dbus.Signal{
		Name: interfacesAdded,
		Body: []interface{}{
			dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/sdb1"),
			map[string]map[string]dbus.Variant{
				blockIf: {},
			},
		},
	}

	event, err := parseInterfacesAdded(sig, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event == nil {
		t.Fatal("expected event, got nil")
		return
	}
	if event.DeviceName != "sdb1" {
		t.Errorf("DeviceName = %q, want %q", event.DeviceName, "sdb1")
	}
	if event.DeviceType != "block" {
		t.Errorf("DeviceType = %q, want %q", event.DeviceType, "block")
	}
	if event.DeviceBus != "" {
		t.Errorf("DeviceBus = %q, want empty", event.DeviceBus)
	}
	if !event.Added {
		t.Error("Added should be true")
	}
	if event.DevicePath != "/org/freedesktop/UDisks2/block_devices/sdb1" {
		t.Errorf("DevicePath = %q", event.DevicePath)
	}
}

func TestParseInterfacesAdded_Drive(t *testing.T) {
	sig := &dbus.Signal{
		Name: interfacesAdded,
		Body: []interface{}{
			dbus.ObjectPath("/org/freedesktop/UDisks2/drives/WD_Elements_1234"),
			map[string]map[string]dbus.Variant{
				driveIf: {},
			},
		},
	}

	event, err := parseInterfacesAdded(sig, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event == nil {
		t.Fatal("expected event, got nil")
		return
	}
	if event.DeviceName != "WD_Elements_1234" {
		t.Errorf("DeviceName = %q, want %q", event.DeviceName, "WD_Elements_1234")
	}
	if event.DeviceType != "drive" {
		t.Errorf("DeviceType = %q, want %q", event.DeviceType, "drive")
	}
	if event.DeviceBus != "" {
		t.Errorf("DeviceBus = %q, want empty (no ConnectionBus property)", event.DeviceBus)
	}
}

func TestParseInterfacesAdded_USBDrive(t *testing.T) {
	sig := &dbus.Signal{
		Name: interfacesAdded,
		Body: []interface{}{
			dbus.ObjectPath("/org/freedesktop/UDisks2/drives/usb_drive_1"),
			map[string]map[string]dbus.Variant{
				driveIf: {
					"ConnectionBus": dbus.MakeVariant("usb"),
				},
			},
		},
	}

	event, err := parseInterfacesAdded(sig, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event == nil {
		t.Fatal("expected event, got nil")
		return
	}
	if event.DeviceType != "drive" {
		t.Errorf("DeviceType = %q, want drive (kind-of-object axis)", event.DeviceType)
	}
	if event.DeviceBus != "usb" {
		t.Errorf("DeviceBus = %q, want usb", event.DeviceBus)
	}
}

func TestParseInterfacesAdded_PartitionUsesLookupForBus(t *testing.T) {
	// A partition signal carries a Drive reference but no ConnectionBus.
	// parseInterfacesAdded should consult lookupBus to fill DeviceBus.
	sig := &dbus.Signal{
		Name: interfacesAdded,
		Body: []interface{}{
			dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/sda2"),
			map[string]map[string]dbus.Variant{
				blockIf: {
					"Drive": dbus.MakeVariant(dbus.ObjectPath("/org/freedesktop/UDisks2/drives/SanDisk_X")),
				},
			},
		},
	}

	var lookedUp string
	lookup := func(p string) string {
		lookedUp = p
		return "usb"
	}

	event, err := parseInterfacesAdded(sig, lookup)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event == nil {
		t.Fatal("expected event")
		return
	}
	if event.DeviceType != "block" {
		t.Errorf("DeviceType = %q, want block (no reclassification)", event.DeviceType)
	}
	if event.DeviceBus != "usb" {
		t.Errorf("DeviceBus = %q, want usb (from lookup)", event.DeviceBus)
	}
	if event.DrivePath != "/org/freedesktop/UDisks2/drives/SanDisk_X" {
		t.Errorf("DrivePath = %q", event.DrivePath)
	}
	if lookedUp != "/org/freedesktop/UDisks2/drives/SanDisk_X" {
		t.Errorf("lookup called with %q, want the Drive path", lookedUp)
	}
}

func TestParseInterfacesAdded_LoopDeviceHasLoopBusAndEmptyDrivePath(t *testing.T) {
	// parseInterfacesAdded itself doesn't suppress empty-BackingFile loops
	// — that filtering happens in Subscribe. This test asserts the parsed
	// shape; for end-to-end suppression behavior see TestSubscribe_Loop*.
	sig := &dbus.Signal{
		Name: interfacesAdded,
		Body: []interface{}{
			dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/loop0"),
			map[string]map[string]dbus.Variant{
				blockIf: {
					"Drive": dbus.MakeVariant(dbus.ObjectPath("/")), // UDisks2 sentinel for "no parent drive"
				},
				loopIf: {},
			},
		},
	}

	event, err := parseInterfacesAdded(sig, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event == nil {
		t.Fatal("expected event")
		return
	}
	if event.DeviceType != "block" {
		t.Errorf("DeviceType = %q, want block", event.DeviceType)
	}
	if event.DeviceBus != "loop" {
		t.Errorf("DeviceBus = %q, want loop", event.DeviceBus)
	}
	if event.DrivePath != "" {
		t.Errorf("DrivePath = %q, want empty (no parent drive)", event.DrivePath)
	}
}

func TestSubscribe_LoopAttachDetachCycle(t *testing.T) {
	// End-to-end: InterfacesAdded for a loop node with empty BackingFile
	// fires no CONNECTED; the subsequent PropertiesChanged that sets
	// BackingFile fires CONNECTED; another PropertiesChanged clearing it
	// fires DISCONNECTED. This is the "node reused across attach/detach
	// cycles" path that the kernel takes after the first allocation.
	conn := newMockConn()
	events, err := Subscribe(conn, "all", "all")
	if err != nil {
		t.Fatalf("Subscribe error: %v", err)
	}

	loopPath := "/org/freedesktop/UDisks2/block_devices/loop0"

	// 1. Node allocation, no backing file yet → should NOT emit.
	conn.signals <- &dbus.Signal{
		Name: interfacesAdded,
		Body: []interface{}{
			dbus.ObjectPath(loopPath),
			map[string]map[string]dbus.Variant{
				blockIf: {"Drive": dbus.MakeVariant(dbus.ObjectPath("/"))},
				loopIf:  {"BackingFile": dbus.MakeVariant([]byte{})},
			},
		},
	}

	// 2. Attach: BackingFile transitions empty → "/tmp/x.img" → CONNECTED.
	conn.signals <- &dbus.Signal{
		Name: propertiesChanged,
		Path: dbus.ObjectPath(loopPath),
		Body: []interface{}{
			loopIf,
			map[string]dbus.Variant{
				"BackingFile": dbus.MakeVariant(append([]byte("/tmp/x.img"), 0)),
			},
			[]string{},
		},
	}

	// 3. Detach: BackingFile transitions "/tmp/x.img" → empty → DISCONNECTED.
	conn.signals <- &dbus.Signal{
		Name: propertiesChanged,
		Path: dbus.ObjectPath(loopPath),
		Body: []interface{}{
			loopIf,
			map[string]dbus.Variant{
				"BackingFile": dbus.MakeVariant([]byte{}),
			},
			[]string{},
		},
	}

	attach := <-events
	if !attach.Added {
		t.Errorf("attach: Added=%v, want true", attach.Added)
	}
	if attach.DeviceBus != "loop" {
		t.Errorf("attach: DeviceBus=%q, want loop", attach.DeviceBus)
	}
	if attach.DevicePath != loopPath {
		t.Errorf("attach: DevicePath=%q", attach.DevicePath)
	}

	detach := <-events
	if detach.Added {
		t.Errorf("detach: Added=%v, want false", detach.Added)
	}
	if detach.DeviceBus != "loop" {
		t.Errorf("detach: DeviceBus=%q, want loop", detach.DeviceBus)
	}

	close(conn.signals)
}

func TestSubscribe_LoopAllocatedWithBackingFileFiresConnected(t *testing.T) {
	// Allocation + attach in the same InterfacesAdded (first-ever attach to
	// a freshly-allocated /dev/loopN) should still emit one CONNECTED.
	conn := newMockConn()
	events, err := Subscribe(conn, "all", "all")
	if err != nil {
		t.Fatalf("Subscribe error: %v", err)
	}

	conn.signals <- &dbus.Signal{
		Name: interfacesAdded,
		Body: []interface{}{
			dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/loop1"),
			map[string]map[string]dbus.Variant{
				blockIf: {"Drive": dbus.MakeVariant(dbus.ObjectPath("/"))},
				loopIf:  {"BackingFile": dbus.MakeVariant(append([]byte("/tmp/y.img"), 0))},
			},
		},
	}

	select {
	case evt := <-events:
		if !evt.Added || evt.DeviceBus != "loop" {
			t.Errorf("event = %+v, want CONNECTED bus=loop", evt)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for loop CONNECTED")
	}

	close(conn.signals)
}

func TestSubscribe_LoopRemoveAfterDetachSuppressed(t *testing.T) {
	// If autoclear is off, detach fires via PropertiesChanged and the
	// kernel later (e.g. on plugin shutdown or losetup -D) may remove the
	// node, firing InterfacesRemoved. That removal should NOT also fire a
	// DISCONNECTED — the user already saw the detach event.
	conn := newMockConn()
	events, err := Subscribe(conn, "all", "all")
	if err != nil {
		t.Fatalf("Subscribe error: %v", err)
	}

	loopPath := "/org/freedesktop/UDisks2/block_devices/loop0"

	conn.signals <- &dbus.Signal{
		Name: interfacesAdded,
		Body: []interface{}{
			dbus.ObjectPath(loopPath),
			map[string]map[string]dbus.Variant{
				blockIf: {"Drive": dbus.MakeVariant(dbus.ObjectPath("/"))},
				loopIf:  {"BackingFile": dbus.MakeVariant(append([]byte("/tmp/x.img"), 0))},
			},
		},
	}
	// Detach via PropertiesChanged → DISCONNECTED #1.
	conn.signals <- &dbus.Signal{
		Name: propertiesChanged,
		Path: dbus.ObjectPath(loopPath),
		Body: []interface{}{
			loopIf,
			map[string]dbus.Variant{
				"BackingFile": dbus.MakeVariant([]byte{}),
			},
			[]string{},
		},
	}
	// Node removal — should be swallowed.
	conn.signals <- &dbus.Signal{
		Name: interfacesRemoved,
		Body: []interface{}{
			dbus.ObjectPath(loopPath),
			[]string{blockIf, loopIf},
		},
	}

	<-events // attach
	<-events // detach (from PropertiesChanged)

	select {
	case evt := <-events:
		t.Errorf("unexpected event after detach: %+v", evt)
	case <-time.After(50 * time.Millisecond):
		// expected
	}

	close(conn.signals)
}

func TestSubscribe_LoopRemoveWhileAttachedFiresDisconnected(t *testing.T) {
	// Autoclear path: device was attached and the kernel removes the node
	// (i.e. backing file still set at removal time). The InterfacesRemoved
	// is the user-visible detach event since no prior PropertiesChanged
	// cleared the BackingFile.
	conn := newMockConn()
	events, err := Subscribe(conn, "all", "all")
	if err != nil {
		t.Fatalf("Subscribe error: %v", err)
	}

	loopPath := "/org/freedesktop/UDisks2/block_devices/loop2"

	conn.signals <- &dbus.Signal{
		Name: interfacesAdded,
		Body: []interface{}{
			dbus.ObjectPath(loopPath),
			map[string]map[string]dbus.Variant{
				blockIf: {"Drive": dbus.MakeVariant(dbus.ObjectPath("/"))},
				loopIf:  {"BackingFile": dbus.MakeVariant(append([]byte("/tmp/z.img"), 0))},
			},
		},
	}
	conn.signals <- &dbus.Signal{
		Name: interfacesRemoved,
		Body: []interface{}{
			dbus.ObjectPath(loopPath),
			[]string{blockIf, loopIf},
		},
	}

	<-events // attach
	detach := <-events
	if detach.Added {
		t.Errorf("expected DISCONNECTED from autoclear removal, got %+v", detach)
	}

	close(conn.signals)
}

func TestHandleLoopPropertiesChanged_NoTransition(t *testing.T) {
	// PropertiesChanged with BackingFile already matching the cache should
	// not emit anything (could happen on UDisks2 redundant signals).
	cache := newBusCache()
	cache.set("/org/freedesktop/UDisks2/block_devices/loop0", busCacheEntry{
		Bus:         "loop",
		BackingFile: "/tmp/same.img",
	})
	sig := &dbus.Signal{
		Name: propertiesChanged,
		Path: dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/loop0"),
		Body: []interface{}{
			loopIf,
			map[string]dbus.Variant{
				"BackingFile": dbus.MakeVariant(append([]byte("/tmp/same.img"), 0)),
			},
			[]string{},
		},
	}
	if e := handleLoopPropertiesChanged(sig, cache); e != nil {
		t.Errorf("expected nil event for no-op transition, got %+v", e)
	}
}

func TestHandleLoopPropertiesChanged_IrrelevantInterface(t *testing.T) {
	// PropertiesChanged for a non-Loop interface should return nil.
	cache := newBusCache()
	sig := &dbus.Signal{
		Name: propertiesChanged,
		Path: dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/sda1"),
		Body: []interface{}{
			filesystemIf,
			map[string]dbus.Variant{
				"MountPoints": dbus.MakeVariant([][]byte{[]byte("/mnt/x")}),
			},
			[]string{},
		},
	}
	if e := handleLoopPropertiesChanged(sig, cache); e != nil {
		t.Errorf("expected nil for non-Loop iface, got %+v", e)
	}
}

func TestHandleLoopPropertiesChanged_NoBackingFileProperty(t *testing.T) {
	// PropertiesChanged on Loop iface but for some other property (e.g.
	// AutoClear changing) should return nil.
	cache := newBusCache()
	sig := &dbus.Signal{
		Name: propertiesChanged,
		Path: dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/loop0"),
		Body: []interface{}{
			loopIf,
			map[string]dbus.Variant{
				"AutoClear": dbus.MakeVariant(true),
			},
			[]string{},
		},
	}
	if e := handleLoopPropertiesChanged(sig, cache); e != nil {
		t.Errorf("expected nil for non-BackingFile property, got %+v", e)
	}
}

func TestExtractBackingFileFromAddedSignal(t *testing.T) {
	tests := []struct {
		name     string
		body     []interface{}
		expected string
	}{
		{
			"with Loop iface + BackingFile",
			[]interface{}{
				dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/loop0"),
				map[string]map[string]dbus.Variant{
					loopIf: {"BackingFile": dbus.MakeVariant(append([]byte("/tmp/a.img"), 0))},
				},
			},
			"/tmp/a.img",
		},
		{
			"with Loop iface but no BackingFile property",
			[]interface{}{
				dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/loop0"),
				map[string]map[string]dbus.Variant{loopIf: {}},
			},
			"",
		},
		{
			"no Loop iface",
			[]interface{}{
				dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/sda1"),
				map[string]map[string]dbus.Variant{blockIf: {}},
			},
			"",
		},
		{
			"malformed body",
			[]interface{}{dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/loop0")},
			"",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sig := &dbus.Signal{Name: interfacesAdded, Body: tc.body}
			got := extractBackingFileFromAddedSignal(sig)
			if got != tc.expected {
				t.Errorf("extractBackingFileFromAddedSignal() = %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestParseInterfacesAdded_WithMountPoint(t *testing.T) {
	mountBytes := append([]byte("/mnt/usb"), 0) // null-terminated
	sig := &dbus.Signal{
		Name: interfacesAdded,
		Body: []interface{}{
			dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/sdc1"),
			map[string]map[string]dbus.Variant{
				blockIf: {},
				filesystemIf: {
					"MountPoints": dbus.MakeVariant([][]byte{mountBytes}),
				},
			},
		},
	}

	event, err := parseInterfacesAdded(sig, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event == nil {
		t.Fatal("expected event, got nil")
		return
	}
	if event.MountPoint != "/mnt/usb" {
		t.Errorf("MountPoint = %q, want %q", event.MountPoint, "/mnt/usb")
	}
}

func TestParseInterfacesAdded_UnrelatedInterface(t *testing.T) {
	sig := &dbus.Signal{
		Name: interfacesAdded,
		Body: []interface{}{
			dbus.ObjectPath("/org/freedesktop/UDisks2/jobs/1"),
			map[string]map[string]dbus.Variant{
				"org.freedesktop.UDisks2.Job": {},
			},
		},
	}

	event, err := parseInterfacesAdded(sig, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event != nil {
		t.Errorf("expected nil event for unrelated interface, got %+v", event)
	}
}

func TestParseInterfacesAdded_InvalidObjectPathType(t *testing.T) {
	sig := &dbus.Signal{
		Name: interfacesAdded,
		Body: []interface{}{
			"not-an-object-path",
			map[string]map[string]dbus.Variant{blockIf: {}},
		},
	}
	_, err := parseInterfacesAdded(sig, nil)
	if err == nil {
		t.Fatal("expected error for invalid object path type")
	}
}

func TestParseInterfacesAdded_InvalidIfacesType(t *testing.T) {
	sig := &dbus.Signal{
		Name: interfacesAdded,
		Body: []interface{}{
			dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/sdb1"),
			"not-a-map",
		},
	}
	_, err := parseInterfacesAdded(sig, nil)
	if err == nil {
		t.Fatal("expected error for invalid interfaces type")
	}
}

func TestParseInterfacesAdded_TooFewBodyElements(t *testing.T) {
	sig := &dbus.Signal{
		Name: interfacesAdded,
		Body: []interface{}{
			dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/sdb1"),
		},
	}

	_, err := parseInterfacesAdded(sig, nil)
	if err == nil {
		t.Fatal("expected error for too few body elements")
	}
}

func TestParseInterfacesRemoved_BlockDevice(t *testing.T) {
	sig := &dbus.Signal{
		Name: interfacesRemoved,
		Body: []interface{}{
			dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/sdb1"),
			[]string{blockIf, filesystemIf},
		},
	}

	event, err := parseInterfacesRemoved(sig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event == nil {
		t.Fatal("expected event, got nil")
		return
	}
	if event.DeviceName != "sdb1" {
		t.Errorf("DeviceName = %q, want %q", event.DeviceName, "sdb1")
	}
	if event.DeviceType != "block" {
		t.Errorf("DeviceType = %q, want %q", event.DeviceType, "block")
	}
	if event.Added {
		t.Error("Added should be false")
	}
}

func TestParseInterfacesRemoved_Drive(t *testing.T) {
	sig := &dbus.Signal{
		Name: interfacesRemoved,
		Body: []interface{}{
			dbus.ObjectPath("/org/freedesktop/UDisks2/drives/WD_Elements_1234"),
			[]string{driveIf},
		},
	}

	event, err := parseInterfacesRemoved(sig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event == nil {
		t.Fatal("expected event, got nil")
		return
	}
	if event.DeviceType != "drive" {
		t.Errorf("DeviceType = %q, want %q", event.DeviceType, "drive")
	}
}

func TestParseInterfacesRemoved_UnrelatedInterface(t *testing.T) {
	sig := &dbus.Signal{
		Name: interfacesRemoved,
		Body: []interface{}{
			dbus.ObjectPath("/org/freedesktop/UDisks2/jobs/1"),
			[]string{"org.freedesktop.UDisks2.Job"},
		},
	}

	event, err := parseInterfacesRemoved(sig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event != nil {
		t.Errorf("expected nil event for unrelated interface, got %+v", event)
	}
}

func TestParseInterfacesRemoved_InvalidObjectPathType(t *testing.T) {
	sig := &dbus.Signal{
		Name: interfacesRemoved,
		Body: []interface{}{
			"not-an-object-path",
			[]string{blockIf},
		},
	}
	_, err := parseInterfacesRemoved(sig)
	if err == nil {
		t.Fatal("expected error for invalid object path type")
	}
}

func TestParseInterfacesRemoved_InvalidIfacesType(t *testing.T) {
	sig := &dbus.Signal{
		Name: interfacesRemoved,
		Body: []interface{}{
			dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/sdb1"),
			42, // not []string
		},
	}
	_, err := parseInterfacesRemoved(sig)
	if err == nil {
		t.Fatal("expected error for invalid interfaces type")
	}
}

func TestParseInterfacesRemoved_TooFewBodyElements(t *testing.T) {
	sig := &dbus.Signal{
		Name: interfacesRemoved,
		Body: []interface{}{
			dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/sdb1"),
		},
	}

	_, err := parseInterfacesRemoved(sig)
	if err == nil {
		t.Fatal("expected error for too few body elements")
	}
}

func TestExtractDeviceName(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/org/freedesktop/UDisks2/block_devices/sdb1", "sdb1"},
		{"/org/freedesktop/UDisks2/drives/WD_Elements_1234", "WD_Elements_1234"},
		{"/org/freedesktop/UDisks2/block_devices/nvme0n1p1", "nvme0n1p1"},
	}

	for _, tc := range tests {
		got := extractDeviceName(tc.path)
		if got != tc.expected {
			t.Errorf("extractDeviceName(%q) = %q, want %q", tc.path, got, tc.expected)
		}
	}
}

func TestExtractMountPoint(t *testing.T) {
	tests := []struct {
		name     string
		props    map[string]dbus.Variant
		expected string
	}{
		{
			"with mount point",
			map[string]dbus.Variant{
				"MountPoints": dbus.MakeVariant([][]byte{append([]byte("/mnt/usb"), 0)}),
			},
			"/mnt/usb",
		},
		{
			"no mount points property",
			map[string]dbus.Variant{},
			"",
		},
		{
			"empty mount points",
			map[string]dbus.Variant{
				"MountPoints": dbus.MakeVariant([][]byte{}),
			},
			"",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractMountPoint(tc.props)
			if got != tc.expected {
				t.Errorf("extractMountPoint() = %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestExtractDrivePath(t *testing.T) {
	tests := []struct {
		name     string
		ifaces   map[string]map[string]dbus.Variant
		expected string
	}{
		{
			"ObjectPath variant",
			map[string]map[string]dbus.Variant{
				blockIf: {"Drive": dbus.MakeVariant(dbus.ObjectPath("/org/freedesktop/UDisks2/drives/X"))},
			},
			"/org/freedesktop/UDisks2/drives/X",
		},
		{
			"sentinel root path treated as empty",
			map[string]map[string]dbus.Variant{
				blockIf: {"Drive": dbus.MakeVariant(dbus.ObjectPath("/"))},
			},
			"",
		},
		{
			"no block interface",
			map[string]map[string]dbus.Variant{},
			"",
		},
		{
			"no Drive property",
			map[string]map[string]dbus.Variant{blockIf: {}},
			"",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractDrivePath(tc.ifaces)
			if got != tc.expected {
				t.Errorf("extractDrivePath() = %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestClassifyDevice_BothInterfaces(t *testing.T) {
	ifaces := map[string]map[string]dbus.Variant{
		blockIf: {},
		driveIf: {},
	}
	got := classifyDevice("/org/freedesktop/UDisks2/block_devices/sda1", ifaces)
	if got != "block" {
		t.Errorf("classifyDevice = %q, want %q", got, "block")
	}
}

func TestClassifyDevice(t *testing.T) {
	tests := []struct {
		name     string
		objPath  string
		ifaces   map[string]map[string]dbus.Variant
		expected string
	}{
		{
			"block device",
			"/org/freedesktop/UDisks2/block_devices/sda1",
			map[string]map[string]dbus.Variant{blockIf: {}},
			"block",
		},
		{
			"drive",
			"/org/freedesktop/UDisks2/drives/myDrive",
			map[string]map[string]dbus.Variant{driveIf: {}},
			"drive",
		},
		{
			"block interface but wrong path",
			"/org/freedesktop/UDisks2/jobs/1",
			map[string]map[string]dbus.Variant{blockIf: {}},
			"",
		},
		{
			"unrelated interface",
			"/org/freedesktop/UDisks2/block_devices/sda1",
			map[string]map[string]dbus.Variant{"org.freedesktop.UDisks2.Job": {}},
			"",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyDevice(tc.objPath, tc.ifaces)
			if got != tc.expected {
				t.Errorf("classifyDevice() = %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestClassifyRemovedDevice(t *testing.T) {
	tests := []struct {
		name     string
		objPath  string
		ifaces   []string
		expected string
	}{
		{
			"block removed",
			"/org/freedesktop/UDisks2/block_devices/sda1",
			[]string{blockIf},
			"block",
		},
		{
			"drive removed",
			"/org/freedesktop/UDisks2/drives/myDrive",
			[]string{driveIf},
			"drive",
		},
		{
			"unrelated removed",
			"/org/freedesktop/UDisks2/jobs/1",
			[]string{"org.freedesktop.UDisks2.Job"},
			"",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyRemovedDevice(tc.objPath, tc.ifaces)
			if got != tc.expected {
				t.Errorf("classifyRemovedDevice() = %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestExtractConnectionBus(t *testing.T) {
	tests := []struct {
		name     string
		ifaces   map[string]map[string]dbus.Variant
		expected string
	}{
		{
			"USB drive",
			map[string]map[string]dbus.Variant{
				driveIf: {"ConnectionBus": dbus.MakeVariant("usb")},
			},
			"usb",
		},
		{
			"SATA drive",
			map[string]map[string]dbus.Variant{
				driveIf: {"ConnectionBus": dbus.MakeVariant("sata")},
			},
			"sata",
		},
		{
			"case-insensitive lowercases USB to usb",
			map[string]map[string]dbus.Variant{
				driveIf: {"ConnectionBus": dbus.MakeVariant("USB")},
			},
			"usb",
		},
		{
			"no ConnectionBus property",
			map[string]map[string]dbus.Variant{driveIf: {}},
			"",
		},
		{
			"USB block-level ConnectionBus fallback",
			map[string]map[string]dbus.Variant{
				blockIf: {"ConnectionBus": dbus.MakeVariant("usb")},
			},
			"usb",
		},
		{
			"Loop interface implies bus=loop",
			map[string]map[string]dbus.Variant{
				blockIf: {},
				loopIf:  {},
			},
			"loop",
		},
		{
			"empty interfaces -> empty bus",
			map[string]map[string]dbus.Variant{},
			"",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractConnectionBus(tc.ifaces)
			if got != tc.expected {
				t.Errorf("extractConnectionBus() = %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestMatchesBus(t *testing.T) {
	tests := []struct {
		name   string
		event  DeviceEvent
		filter string
		want   bool
	}{
		{"all matches usb", DeviceEvent{DeviceBus: "usb"}, "all", true},
		{"all matches empty", DeviceEvent{DeviceBus: ""}, "all", true},
		{"empty filter matches anything", DeviceEvent{DeviceBus: "loop"}, "", true},
		{"usb filter matches usb event", DeviceEvent{DeviceBus: "usb"}, "usb", true},
		{"usb filter rejects sata", DeviceEvent{DeviceBus: "sata"}, "usb", false},
		{"usb filter rejects empty bus event", DeviceEvent{DeviceBus: ""}, "usb", false},
		{"case-insensitive", DeviceEvent{DeviceBus: "usb"}, "USB", true},
		{"loop filter matches loop", DeviceEvent{DeviceBus: "loop"}, "loop", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesBus(&tt.event, tt.filter)
			if got != tt.want {
				t.Errorf("matchesBus(bus=%q, filter=%q) = %v, want %v", tt.event.DeviceBus, tt.filter, got, tt.want)
			}
		})
	}
}

func TestExtractMountPoint_NonByteSlice(t *testing.T) {
	props := map[string]dbus.Variant{
		"MountPoints": dbus.MakeVariant("not-byte-slices"),
	}
	got := extractMountPoint(props)
	if got != "" {
		t.Errorf("expected empty for non-[][]byte, got %q", got)
	}
}
