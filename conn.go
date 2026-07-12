//go:build linux

package main

import "github.com/godbus/dbus/v5"

// DBusConn abstracts the D-Bus connection methods used by Subscribe.
// *dbus.Conn satisfies this interface.
type DBusConn interface {
	AddMatchSignal(options ...dbus.MatchOption) error
	Signal(ch chan<- *dbus.Signal)
	// Object returns a remote D-Bus object proxy. Used to read the
	// ConnectionBus property of a partition's parent Drive — UDisks2 only
	// puts ConnectionBus on the Drive object, not on partition Block
	// objects, so partitions can't be classified as "usb" without this
	// out-of-band lookup. *dbus.Conn already exposes this method.
	Object(dest string, path dbus.ObjectPath) dbus.BusObject
}
