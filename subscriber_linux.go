//go:build linux

package main

import "github.com/godbus/dbus/v5"

// dbusSubscriber implements DeviceSubscriber using UDisks2 D-Bus signals.
type dbusSubscriber struct {
	conn *dbus.Conn
}

func newSubscriber() (DeviceSubscriber, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, err
	}
	return &dbusSubscriber{conn: conn}, nil
}

func (s *dbusSubscriber) Subscribe(deviceType, deviceBus string) (<-chan DeviceEvent, error) {
	return Subscribe(s.conn, deviceType, deviceBus)
}

func (s *dbusSubscriber) Close() {
	_ = s.conn.Close()
}
