//go:build linux

package main

import (
	"bytes"
	"fmt"
	"path"
	"strings"
	"sync"

	"github.com/godbus/dbus/v5"
)

const (
	udisks2Bus        = "org.freedesktop.UDisks2"
	udisks2Path       = "/org/freedesktop/UDisks2"
	objectManagerIf   = "org.freedesktop.DBus.ObjectManager"
	blockIf           = "org.freedesktop.UDisks2.Block"
	driveIf           = "org.freedesktop.UDisks2.Drive"
	filesystemIf      = "org.freedesktop.UDisks2.Filesystem"
	loopIf            = "org.freedesktop.UDisks2.Loop"
	propertiesIf      = "org.freedesktop.DBus.Properties"
	interfacesAdded   = objectManagerIf + ".InterfacesAdded"
	interfacesRemoved = objectManagerIf + ".InterfacesRemoved"
	propertiesChanged = propertiesIf + ".PropertiesChanged"
)

// busCacheEntry stores the per-device state we need to replay on remove
// and to detect loop attach/detach transitions.
//
// InterfacesRemoved signals carry no properties, so the only way to report
// a removal with its original bus/drive context is to remember it at add
// time and look it up on remove.
//
// BackingFile tracks the current state of the UDisks2.Loop interface's
// BackingFile property. Loop devices reuse `/dev/loopN` nodes across
// attach/detach cycles — only the first attach fires `InterfacesAdded` and
// only autoclear-mode detach fires `InterfacesRemoved`. The user-visible
// attach/detach events come through as `PropertiesChanged` on BackingFile,
// and we synthesize CONNECTED/DISCONNECTED from those transitions.
type busCacheEntry struct {
	Bus         string // ConnectionBus value ("usb", "loop", "sata", ...)
	DrivePath   string // for block devices: the parent drive path
	BackingFile string // for loop devices: current backing file path; empty = detached
}

// busCache is a small in-memory map keyed by UDisks2 object path.
//
// It serves two purposes beyond the remove-time replay:
//
//  1. Block events don't carry ConnectionBus (that property lives on the
//     parent Drive object). When the partition's InterfacesAdded fires, we
//     look up the parent drive's cached entry to inherit its bus — fast,
//     no D-Bus round-trip needed because the drive event fired first.
//  2. If the drive's add was missed (e.g. plugin started while the device
//     was already attached), an out-of-band GetProperty fallback in
//     makeDriveBusLookup queries UDisks2 directly.
type busCache struct {
	mu     sync.Mutex
	byPath map[string]busCacheEntry
}

func newBusCache() *busCache {
	return &busCache{byPath: make(map[string]busCacheEntry)}
}

func (c *busCache) get(p string) (busCacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.byPath[p]
	return v, ok
}

func (c *busCache) set(p string, entry busCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byPath[p] = entry
}

func (c *busCache) evict(p string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.byPath, p)
}

// Subscribe connects to UDisks2 signals on the system bus and returns a
// channel of DeviceEvents filtered by both axes. The channel is closed when
// the D-Bus connection is closed.
func Subscribe(conn DBusConn, deviceType, deviceBus string) (<-chan DeviceEvent, error) {
	if err := conn.AddMatchSignal(
		dbus.WithMatchObjectPath(udisks2Path),
		dbus.WithMatchInterface(objectManagerIf),
		dbus.WithMatchMember("InterfacesAdded"),
	); err != nil {
		return nil, fmt.Errorf("adding InterfacesAdded match: %w", err)
	}

	if err := conn.AddMatchSignal(
		dbus.WithMatchObjectPath(udisks2Path),
		dbus.WithMatchInterface(objectManagerIf),
		dbus.WithMatchMember("InterfacesRemoved"),
	); err != nil {
		return nil, fmt.Errorf("adding InterfacesRemoved match: %w", err)
	}

	// PropertiesChanged is needed to observe loop device attach/detach when
	// the kernel reuses an existing /dev/loopN node (no InterfacesAdded
	// fires for re-attaches; no InterfacesRemoved fires for non-autoclear
	// detaches). We match broadly and filter in handleLoopPropertiesChanged.
	if err := conn.AddMatchSignal(
		dbus.WithMatchInterface(propertiesIf),
		dbus.WithMatchMember("PropertiesChanged"),
	); err != nil {
		return nil, fmt.Errorf("adding PropertiesChanged match: %w", err)
	}

	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)

	cache := newBusCache()
	lookupBus := makeDriveBusLookup(conn, cache)

	events := make(chan DeviceEvent, 16)
	go func() {
		defer close(events)
		for sig := range signals {
			var event *DeviceEvent
			var err error

			switch sig.Name {
			case interfacesAdded:
				event, err = parseInterfacesAdded(sig, lookupBus)
				if event != nil {
					backing := extractBackingFileFromAddedSignal(sig)
					cache.set(event.DevicePath, busCacheEntry{
						Bus:         event.DeviceBus,
						DrivePath:   event.DrivePath,
						BackingFile: backing,
					})
					// Loop nodes that appear without a backing file aren't
					// yet "connected" from a user's perspective — the
					// PropertiesChanged that follows when a file is
					// attached will fire the CONNECTED event. Suppress
					// this premature one.
					if event.DeviceBus == "loop" && backing == "" {
						event = nil
					}
				}
			case interfacesRemoved:
				event, err = parseInterfacesRemoved(sig)
				if event != nil {
					path := event.DevicePath
					if cached, ok := cache.get(path); ok {
						event.DeviceBus = cached.Bus
						event.DrivePath = cached.DrivePath
						// Loop nodes that were already detached when the
						// kernel removed them: the DISCONNECTED already
						// fired via PropertiesChanged — suppress.
						if cached.Bus == "loop" && cached.BackingFile == "" {
							event = nil
						}
						cache.evict(path)
					}
				}
			case propertiesChanged:
				event = handleLoopPropertiesChanged(sig, cache)
			default:
				continue
			}

			if err != nil || event == nil {
				continue
			}

			if !matchesDeviceType(event, deviceType) || !matchesBus(event, deviceBus) {
				continue
			}

			events <- *event
		}
	}()

	return events, nil
}

// handleLoopPropertiesChanged synthesizes a CONNECTED or DISCONNECTED event
// from a PropertiesChanged signal whose BackingFile property transitioned
// across the empty boundary. Other PropertiesChanged signals (and other
// properties on the Loop interface) return nil.
//
// Updates the cache with the new BackingFile value so subsequent transitions
// can be detected.
func handleLoopPropertiesChanged(sig *dbus.Signal, cache *busCache) *DeviceEvent {
	if len(sig.Body) < 2 {
		return nil
	}
	ifaceName, ok := sig.Body[0].(string)
	if !ok || ifaceName != loopIf {
		return nil
	}
	changed, ok := sig.Body[1].(map[string]dbus.Variant)
	if !ok {
		return nil
	}
	bf, hasBF := changed["BackingFile"]
	if !hasBF {
		return nil
	}
	objPath := string(sig.Path)
	if !strings.Contains(objPath, "/block_devices/") {
		return nil
	}
	newBacking := decodeBackingFile(bf)

	cached, hadEntry := cache.get(objPath)
	if !hadEntry {
		// Loop iface seen via PropertiesChanged before any InterfacesAdded
		// reached us (e.g. plugin started while the device existed).
		// Default the bus to "loop" so the synthesized event filters
		// correctly.
		cached = busCacheEntry{Bus: "loop"}
	}
	oldBacking := cached.BackingFile
	if oldBacking == newBacking {
		return nil
	}

	cached.BackingFile = newBacking
	cache.set(objPath, cached)

	event := &DeviceEvent{
		DeviceName: extractDeviceName(objPath),
		DeviceType: "block",
		DeviceBus:  cached.Bus,
		DevicePath: objPath,
		DrivePath:  cached.DrivePath,
	}
	if oldBacking == "" && newBacking != "" {
		event.Added = true
		return event
	}
	if oldBacking != "" && newBacking == "" {
		event.Added = false
		return event
	}
	return nil
}

// extractBackingFileFromAddedSignal pulls Loop.BackingFile from an
// InterfacesAdded body. Returns "" when the signal isn't malformed,
// doesn't carry a Loop interface, or carries one with no BackingFile.
func extractBackingFileFromAddedSignal(sig *dbus.Signal) string {
	if len(sig.Body) < 2 {
		return ""
	}
	ifaces, ok := sig.Body[1].(map[string]map[string]dbus.Variant)
	if !ok {
		return ""
	}
	loopProps, ok := ifaces[loopIf]
	if !ok {
		return ""
	}
	bf, ok := loopProps["BackingFile"]
	if !ok {
		return ""
	}
	return decodeBackingFile(bf)
}

// decodeBackingFile decodes the byte-array BackingFile property to a string,
// trimming the trailing NUL byte UDisks2 appends.
func decodeBackingFile(v dbus.Variant) string {
	bs, ok := v.Value().([]byte)
	if !ok {
		return ""
	}
	return string(bytes.TrimRight(bs, "\x00"))
}

// driveBusLookup returns the ConnectionBus value (e.g. "usb") for a UDisks2
// drive object path, or "" if it can't be determined.
type driveBusLookup func(drivePath string) string

// makeDriveBusLookup returns a closure that resolves a drive's connection
// bus, preferring the in-memory cache populated by the drive's own
// InterfacesAdded signal. Falls back to an out-of-band GetProperty when the
// cache misses (e.g. the drive existed before the plugin started).
func makeDriveBusLookup(conn DBusConn, cache *busCache) driveBusLookup {
	return func(drivePath string) string {
		if drivePath == "" {
			return ""
		}
		if entry, ok := cache.get(drivePath); ok {
			return entry.Bus
		}
		obj := conn.Object(udisks2Bus, dbus.ObjectPath(drivePath))
		if obj == nil {
			return ""
		}
		v, err := obj.GetProperty(driveIf + ".ConnectionBus")
		if err != nil {
			return ""
		}
		bus, _ := v.Value().(string)
		return strings.ToLower(bus)
	}
}

// parseInterfacesAdded extracts a DeviceEvent from an InterfacesAdded signal.
// Returns nil if the signal doesn't contain a block device or drive interface.
//
// lookupBus, if non-nil, is used to resolve a partition's connection bus by
// asking its parent Drive (Block signals don't carry ConnectionBus). Tests
// that don't care about per-block bus inheritance can pass nil.
//
// Signal body: [object_path (dbus.ObjectPath), interfaces_and_properties (map[string]map[string]dbus.Variant)]
func parseInterfacesAdded(sig *dbus.Signal, lookupBus driveBusLookup) (*DeviceEvent, error) {
	if len(sig.Body) < 2 {
		return nil, fmt.Errorf("InterfacesAdded: expected 2 body elements, got %d", len(sig.Body))
	}

	objPath, ok := sig.Body[0].(dbus.ObjectPath)
	if !ok {
		return nil, fmt.Errorf("InterfacesAdded: body[0] is not ObjectPath")
	}

	ifaces, ok := sig.Body[1].(map[string]map[string]dbus.Variant)
	if !ok {
		return nil, fmt.Errorf("InterfacesAdded: body[1] is not map[string]map[string]Variant")
	}

	deviceType := classifyDevice(string(objPath), ifaces)
	if deviceType == "" {
		return nil, nil // not a device we care about
	}

	event := &DeviceEvent{
		DeviceName: extractDeviceName(string(objPath)),
		DeviceType: deviceType,
		DevicePath: string(objPath),
		DeviceBus:  extractConnectionBus(ifaces),
		Added:      true,
	}

	if deviceType == "block" {
		event.DrivePath = extractDrivePath(ifaces)
		if event.DeviceBus == "" && event.DrivePath != "" && lookupBus != nil {
			event.DeviceBus = lookupBus(event.DrivePath)
		}
	}

	if fsProps, hasFSIf := ifaces[filesystemIf]; hasFSIf {
		event.MountPoint = extractMountPoint(fsProps)
	}

	return event, nil
}

// extractDrivePath reads the parent Drive object path from a Block
// interface's `Drive` property. UDisks2 uses "/" as the sentinel for "no
// parent drive" (loop devices, for instance) — that's treated as empty
// here so downstream code can use `DrivePath != ""` as a presence check.
func extractDrivePath(ifaces map[string]map[string]dbus.Variant) string {
	blockProps, ok := ifaces[blockIf]
	if !ok {
		return ""
	}
	v, ok := blockProps["Drive"]
	if !ok {
		return ""
	}
	switch p := v.Value().(type) {
	case dbus.ObjectPath:
		s := string(p)
		if s == "/" {
			return ""
		}
		return s
	case string:
		if p == "/" {
			return ""
		}
		return p
	}
	return ""
}

// parseInterfacesRemoved extracts a DeviceEvent from an InterfacesRemoved signal.
// Returns nil if the signal doesn't reference a block device or drive interface.
//
// Signal body: [object_path (dbus.ObjectPath), interfaces ([]string)]
func parseInterfacesRemoved(sig *dbus.Signal) (*DeviceEvent, error) {
	if len(sig.Body) < 2 {
		return nil, fmt.Errorf("InterfacesRemoved: expected 2 body elements, got %d", len(sig.Body))
	}

	objPath, ok := sig.Body[0].(dbus.ObjectPath)
	if !ok {
		return nil, fmt.Errorf("InterfacesRemoved: body[0] is not ObjectPath")
	}

	ifaces, ok := sig.Body[1].([]string)
	if !ok {
		return nil, fmt.Errorf("InterfacesRemoved: body[1] is not []string")
	}

	deviceType := classifyRemovedDevice(string(objPath), ifaces)
	if deviceType == "" {
		return nil, nil
	}

	return &DeviceEvent{
		DeviceName: extractDeviceName(string(objPath)),
		DeviceType: deviceType,
		DevicePath: string(objPath),
		Added:      false,
	}, nil
}

// classifyDevice determines the device type from the object path and interfaces.
func classifyDevice(objPath string, ifaces map[string]map[string]dbus.Variant) string {
	if _, ok := ifaces[blockIf]; ok {
		if strings.Contains(objPath, "/block_devices/") {
			return "block"
		}
	}
	if _, ok := ifaces[driveIf]; ok {
		if strings.Contains(objPath, "/drives/") {
			return "drive"
		}
	}
	return ""
}

// classifyRemovedDevice determines the device type from the object path and
// removed interface names.
func classifyRemovedDevice(objPath string, ifaces []string) string {
	for _, iface := range ifaces {
		if iface == blockIf && strings.Contains(objPath, "/block_devices/") {
			return "block"
		}
		if iface == driveIf && strings.Contains(objPath, "/drives/") {
			return "drive"
		}
	}
	return ""
}

// extractConnectionBus reads the ConnectionBus property from whichever
// interface in the InterfacesAdded payload carries it (typically the Drive
// interface; rarely the Block interface). Returns the lowercased bus name
// or "" when no ConnectionBus is present.
//
// As a special case, devices that expose the UDisks2.Loop interface report
// bus="loop" — UDisks2 doesn't set ConnectionBus on synthetic block
// devices, but the Loop interface's presence is unambiguous.
func extractConnectionBus(ifaces map[string]map[string]dbus.Variant) string {
	if driveProps, ok := ifaces[driveIf]; ok {
		if bus := readBusProperty(driveProps); bus != "" {
			return bus
		}
	}
	if blockProps, ok := ifaces[blockIf]; ok {
		if bus := readBusProperty(blockProps); bus != "" {
			return bus
		}
	}
	if _, ok := ifaces[loopIf]; ok {
		return "loop"
	}
	return ""
}

func readBusProperty(props map[string]dbus.Variant) string {
	v, ok := props["ConnectionBus"]
	if !ok {
		return ""
	}
	s, ok := v.Value().(string)
	if !ok {
		return ""
	}
	return strings.ToLower(s)
}

// extractDeviceName returns the last path component from a D-Bus object path.
func extractDeviceName(objPath string) string {
	return path.Base(objPath)
}

// extractMountPoint decodes the first mount point from a Filesystem's
// MountPoints property. Mount points are byte arrays (null-terminated).
func extractMountPoint(fsProps map[string]dbus.Variant) string {
	mp, ok := fsProps["MountPoints"]
	if !ok {
		return ""
	}

	if points, ok := mp.Value().([][]byte); ok && len(points) > 0 {
		return string(bytes.TrimRight(points[0], "\x00"))
	}
	return ""
}
