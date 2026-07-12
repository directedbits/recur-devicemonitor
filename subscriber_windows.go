//go:build windows

package main

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/yusufpapurcu/wmi"
)

// Win32_LogicalDisk holds WMI fields for drive letter and type information.
type Win32_LogicalDisk struct {
	DeviceID    string // Drive letter e.g. "D:"
	DriveType   uint32 // 2=Removable, 3=Fixed, 4=Network, 5=CD-ROM
	VolumeName  string
	FileSystem  string
	Size        uint64
	Description string
}

// Win32_DiskDrive holds WMI fields for physical disk metadata.
type Win32_DiskDrive struct {
	DeviceID      string
	Model         string
	InterfaceType string // "USB", "IDE", "SCSI", etc.
	Index         uint32
	Size          uint64
}

// wmiSubscriber implements DeviceSubscriber by polling WMI for drive changes.
type wmiSubscriber struct {
	events chan DeviceEvent
	done   chan struct{}
	once   sync.Once
}

func newSubscriber() (DeviceSubscriber, error) {
	return &wmiSubscriber{
		events: make(chan DeviceEvent, 16),
		done:   make(chan struct{}),
	}, nil
}

func (s *wmiSubscriber) Subscribe(deviceType, deviceBus string) (<-chan DeviceEvent, error) {
	currentDrives, err := enumerateDrives()
	if err != nil {
		return nil, fmt.Errorf("initial drive enumeration: %w", err)
	}

	go s.pollLoop(deviceType, deviceBus, currentDrives)
	return s.events, nil
}

func (s *wmiSubscriber) Close() {
	s.once.Do(func() {
		close(s.done)
	})
}

func (s *wmiSubscriber) pollLoop(deviceType, deviceBus string, previousDrives map[string]driveInfo) {
	defer close(s.events)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			currentDrives, err := enumerateDrives()
			if err != nil {
				continue
			}

			added, removed := diffDrives(previousDrives, currentDrives, deviceType, deviceBus)

			for _, event := range added {
				select {
				case s.events <- event:
				case <-s.done:
					return
				}
			}
			for _, event := range removed {
				select {
				case s.events <- event:
				case <-s.done:
					return
				}
			}

			previousDrives = currentDrives

		case <-s.done:
			return
		}
	}
}

// enumerateDrives queries WMI for current logical disks and physical drive
// metadata, returning a snapshot keyed by drive letter.
func enumerateDrives() (map[string]driveInfo, error) {
	var disks []Win32_LogicalDisk
	if err := wmi.Query("SELECT DeviceID, DriveType, VolumeName, FileSystem, Size, Description FROM Win32_LogicalDisk WHERE DriveType = 2 OR DriveType = 3", &disks); err != nil {
		return nil, err
	}

	// Query physical drives for interface type (USB vs IDE etc.)
	var drives []Win32_DiskDrive
	_ = wmi.Query("SELECT DeviceID, Model, InterfaceType, Index FROM Win32_DiskDrive", &drives)

	result := make(map[string]driveInfo)
	for _, disk := range disks {
		info := driveInfo{
			deviceID:  disk.DeviceID,
			model:     disk.VolumeName,
			driveType: disk.DriveType,
		}
		if info.model == "" {
			info.model = disk.Description
		}
		if info.model == "" {
			info.model = disk.DeviceID
		}

		// Check if any physical drive has a USB interface type
		for _, d := range drives {
			if strings.EqualFold(d.InterfaceType, "USB") {
				info.interfaceType = "USB"
				info.model = d.Model
				break
			}
		}

		result[disk.DeviceID] = info
	}

	return result, nil
}
