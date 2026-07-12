---
title: "Device Monitor"
weight: 5
description: "USB/device hotplug triggers (Linux + Windows)"
---

# Device Monitor Plugin

Device connect/disconnect triggers for USB drives, block devices, and other storage. Uses UDisks2 D-Bus signals on Linux and WMI polling on Windows.

## Requirements

### Linux

- `udisks2` must be installed and running (`systemctl status udisks2`)

### Windows

- No external dependencies (uses built-in WMI via COM)
- Not available in WSL2 (WMI is a native Windows API)

### macOS

Not supported. macOS uses DiskArbitration for device events, which has no implementation yet.

## Triggers

### DeviceConnected

Fires when a block device, USB device, or drive is connected.

| Option | Required | Default | Description |
|--------|----------|---------|-------------|
| `device_type` | no | `all` | Filter: `usb`, `block`, `drive`, or `all` |

### DeviceDisconnected

Fires when a block device, USB device, or drive is disconnected.

| Option | Required | Default | Description |
|--------|----------|---------|-------------|
| `device_type` | no | `all` | Filter: `usb`, `block`, `drive`, or `all` |

## Context Variables

| Variable | Description |
|----------|-------------|
| `DeviceName` | Device name (e.g., `sdb1` on Linux, volume name on Windows) |
| `DeviceType` | Classification: `usb`, `block`, or `drive` |
| `DevicePath` | D-Bus object path (Linux) or drive letter (Windows) |
| `MountPoint` | Mount point if mounted, empty otherwise (DeviceConnected only) |

## Device Types

- **`usb`** — USB-connected storage (detected via `ConnectionBus` on Linux, `InterfaceType` or `DriveType=2` on Windows)
- **`block`** — Block devices (`/org/freedesktop/UDisks2/block_devices/` on Linux, `DriveType=3` fixed disks on Windows)
- **`drive`** — Drive objects (`/org/freedesktop/UDisks2/drives/` on Linux, other drive types on Windows)
- **`all`** — All of the above

A USB drive will be classified as `usb` rather than `drive` or `block` when the connection type indicates USB.

## Platform Notes

### Linux

- Uses real-time D-Bus signal subscription (instant event delivery)
- Device name is the kernel name (e.g., `sdb1`, `nvme0n1p1`)
- Device path is the UDisks2 D-Bus object path

### Windows

- Polls WMI every 2 seconds for drive changes (compares snapshots of `Win32_LogicalDisk`)
- Queries `Win32_DiskDrive` for physical drive metadata (interface type, model)
- Drive letters (e.g., `D:`, `E:`) appear in both `DevicePath` and `MountPoint`
- `DeviceName` shows the volume name, drive description, or drive letter
- `DriveType=2` (removable) is classified as `usb`, `DriveType=3` (fixed) as `block`

## Examples

### Auto-backup on USB insert

```yaml
USBBackup:
  on:
    - type: DeviceConnected
      options:
        device_type: "usb"
  do:
    - shell: "rsync -a /home/user/documents/ {{ .MountPoint }}/backup/"
```

### Log all device events

```yaml
DeviceLog:
  on:
    - type: DeviceConnected
    - type: DeviceDisconnected
  do:
    - shell: "echo '{{ .DeviceType }} {{ .DeviceName }} at {{ .DevicePath }}' >> /var/log/devices.log"
```

### Notify on any block device

```yaml
BlockNotify:
  on:
    - type: DeviceConnected
      options:
        device_type: "block"
  do:
    - shell: "notify-send 'Device Connected' '{{ .DeviceName }} ({{ .DeviceType }})'"
```

### Unmount safety check on disconnect

```yaml
SafeRemove:
  on:
    - type: DeviceDisconnected
      options:
        device_type: "usb"
  do:
    - shell: "echo 'USB removed: {{ .DeviceName }}' | wall"
```
