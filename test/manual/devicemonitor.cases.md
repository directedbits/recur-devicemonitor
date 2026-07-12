# Device Monitor Plugin - Manual Test Cases (Linux)

Minimal set of manual tests to cover the plugin surface on Linux. The plugin
uses real-time UDisks2 D-Bus signals — events arrive immediately, no polling.

## Setup

Requires `udisks2` installed and running:

```sh
systemctl status udisks2          # must be active
task build:devicemonitor
mkdir -p ~/.config/recur/plugins/devicemonitor
cp bin/plugins/devicemonitor/* ~/.config/recur/plugins/devicemonitor/
```

Helpful side-channel for cross-checking what UDisks2 actually emits while you
test (run in a separate terminal):

```sh
udisksctl monitor
```

You'll need:
- A USB drive you can plug/unplug.
- `sudo` access to `losetup` for the loop-device tests.

### Loop device hygiene

The plugin synthesizes loop attach/detach events from UDisks2's
`PropertiesChanged` signals on the `Loop.BackingFile` property, so the
underlying kernel behavior (node reuse, autoclear, etc.) is invisible
to recur consumers. Each `losetup --show -f <file>` produces exactly
one `DeviceConnected` event, and each `losetup -d <dev>` produces
exactly one `DeviceDisconnected` event, regardless of whether the
kernel allocates a new `/dev/loopN` or reuses an existing one.

Practical notes:

- `sudo losetup --show -f <file>` prints the allocated loop device
  path. Capture it: `LOOP=$(sudo losetup --show -f /tmp/loop.img)`.
- `sudo losetup -d "$LOOP"` detaches and fires `DeviceDisconnected`.
- `losetup -l` lists currently-attached loops if you want to inspect
  state. `sudo losetup -D` detaches everything.
- `udisksctl monitor` in another terminal shows the raw UDisks2
  signals if you want to confirm what the plugin sees.

## Event model: two orthogonal axes

The plugin emits events with two orthogonal classification axes:

- **`DeviceType`** is the *kind* of UDisks2 object: `drive` or `block`.
- **`DeviceBus`** is the *connection bus*: `usb`, `loop`, `sata`, `ata`,
  `nvme`, ... — whatever UDisks2 reports via `ConnectionBus`. May be empty
  when the bus is unknown.

Block events also carry **`DrivePath`** — the D-Bus object path of the
parent drive — so consumers can correlate the burst of partition events
back to the single drive event that introduced them.

A single USB stick insertion produces this sequence:

| UDisks2 signal | DeviceType | DeviceBus | DrivePath |
|---|---|---|---|
| `drives/<id>` add | `drive` | `usb` | `""` |
| `block_devices/sda` add | `block` | `usb` | `/drives/<id>` |
| `block_devices/sda1` add | `block` | `usb` | `/drives/<id>` |
| `block_devices/sdaN` add | `block` | `usb` | `/drives/<id>` |
| (removals: reverse order, same fields replayed from cache) |

For tests below, "expect CONNECTED" means **one or more** CONNECTED lines —
the exact count depends on how many partitions the drive has.

### Migration note

Earlier versions of this plugin emitted `DeviceType=usb` (overloaded
axis). If you have recurfiles with `device_type: usb`, change them to
`device_bus: usb` (and optionally narrow to `device_type: drive` if you
want exactly one event per insert).

### Default debounce is 0

The plugin manifest sets `debounce: "0"` as the default for both
`DeviceConnected` and `DeviceDisconnected`. The daemon-wide default
(300ms) would otherwise collapse UDisks2's ~100ms insert burst into a
single trailing event, masking per-partition visibility.

Override precedence (least → most specific):

```
daemon-global  <  plugin manifest  <  daemon plugins.<ns>.trigger_defaults  <  group  <  trigger
```

To get a single summary event per device action instead of the full
burst, set `debounce: "300ms"` (or any value larger than your burst
window) on the trigger in your recurfile, or globally for the plugin in
daemon config:

```yaml
# ~/.config/recur/config.yaml
plugins:
  core.devicemonitor:
    trigger_defaults:
      debounce: "300ms"
```

### `MountPoint` on connect is usually empty

UDisks2's `InterfacesAdded` for a partition fires *before* the auto-mount
job completes. The Filesystem interface is present in that signal but its
`MountPoints` property is empty. Mount points populate ~30-50ms later via
a `PropertiesChanged` signal which the plugin does not currently observe,
so the `MountPoint` context variable will typically be empty on the
initial CONNECTED event. To see the mount point, either listen
side-band (`lsblk`, `udisksctl info`) or increase the trigger's debounce
beyond the mount latency so the trailing event reflects the mounted
state.

### Orchestration recipe: correlate the burst

Use the drive event as a "session" identifier and group the block events
by `DrivePath`:

```yaml
USBSession:
  on:
    - type: DeviceConnected
      options:
        device_type: drive
        device_bus: usb
      do:
        - shell: "echo 'USB drive attached: {{.DevicePath}}' >> usb.log"
    - type: DeviceConnected
      options:
        device_type: block
        device_bus: usb
      do:
        - shell: "echo 'partition {{.DeviceName}} of {{.DrivePath}}' >> usb.log"
```

The drive event fires once per insert; the block events fire once per
partition, each carrying `DrivePath` for grouping.

## Test 1: DeviceConnected + DeviceDisconnected (default options)

Covers: both trigger types, default `device_type=all` and `device_bus=all`, all context variables.

```yaml
# ~/test-devmon/recur.yaml
DeviceLog:
  on:
    - type: DeviceConnected
      do:
        - shell: "echo 'CONNECTED: name={{.DeviceName}} type={{.DeviceType}} bus={{.DeviceBus}} path={{.DevicePath}} drive={{.DrivePath}} mount={{.MountPoint}}' >> connected.results"
    - type: DeviceDisconnected
      do:
        - shell: "echo 'DISCONNECTED: name={{.DeviceName}} type={{.DeviceType}} bus={{.DeviceBus}} path={{.DevicePath}} drive={{.DrivePath}}' >> disconnected.results"
```

```sh
cd ~/test-devmon && recur start --foreground &
recur register

# Insert a USB drive        -> expect a burst of CONNECTED lines:
#   - name=<drive-id>  type=drive  bus=usb  path=.../drives/<id>           drive=          mount=
#   - name=sdX         type=block  bus=usb  path=.../block_devices/sdX     drive=.../drives/<id>  mount=
#   - name=sdX1        type=block  bus=usb  path=.../block_devices/sdX1    drive=.../drives/<id>  mount=   (empty: see note above)
#   (one extra line per additional partition)
# Remove the same USB drive -> expect a corresponding burst of DISCONNECTED lines.
# DeviceBus and DrivePath are replayed from the cache populated at add time.
```

Verify:
- `DevicePath` is a UDisks2 D-Bus object path, not a `/dev/...` path.
- Drive event has empty `DrivePath`; block events have it populated.
- `MountPoint` is typically empty on the initial CONNECTED event even for partitions that will auto-mount.
- `DeviceName` is the kernel name for block devices (`sdX`, `sdX1`) and the UDisks drive id for the drive object.

## Test 2: device_bus filter (usb only)

Covers: `device_bus` option filtering, USB classification across drive and block events, per-trigger options.

```yaml
# ~/test-devmon2/recur.yaml
USBOnly:
  on:
    - type: DeviceConnected
      options:
        device_bus: "usb"
      do:
        - shell: "echo 'USB CONNECTED: type={{.DeviceType}} name={{.DeviceName}} at {{.MountPoint}}' >> usb.results"
    - type: DeviceDisconnected
      options:
        device_bus: "usb"
      do:
        - shell: "echo 'USB DISCONNECTED: type={{.DeviceType}} name={{.DeviceName}}' >> usb.results"
```

```sh
cd ~/test-devmon2 && recur start --foreground &
recur register

# Insert a USB drive -> expect one drive line plus one block line per partition,
#                      all bus=usb.
# Remove the USB drive -> matching disconnect lines.

# Negative case: loop devices report bus=loop, not bus=usb, so they
# should NOT fire this trigger.
truncate -s 64M /tmp/loop.img
LOOP=$(sudo losetup --show -f /tmp/loop.img)  # -> expect NO event
sudo losetup -d "$LOOP"                       # -> expect NO event
rm /tmp/loop.img
```

## Test 3: device_type filter (block only)

Covers: kind-of-object filter is now bus-agnostic — USB partitions AND loop
devices both pass `device_type: block`.

```yaml
# ~/test-devmon3/recur.yaml
BlockOnly:
  on:
    - type: DeviceConnected
      options:
        device_type: "block"
      do:
        - shell: "echo 'BLOCK: {{.DeviceName}} bus={{.DeviceBus}} drive={{.DrivePath}}' >> block.results"
```

```sh
cd ~/test-devmon3 && recur start --foreground &
recur register

# Loop device: expect BLOCK lines with bus=loop, drive empty.
truncate -s 64M /tmp/loop.img
LOOP=$(sudo losetup --show -f /tmp/loop.img)  # -> BLOCK: <basename of $LOOP> bus=loop drive=
sudo losetup -d "$LOOP"                       # -> no DeviceConnected (this trigger is connect-only)
rm /tmp/loop.img

# USB stick: expect BLOCK lines for sda and each partition, all with
# bus=usb and DrivePath populated. The drive object is filtered out
# because device_type=block excludes it.
```

## Test 4: device_bus filter (loop only)

Covers: bus filter for non-USB devices.

```yaml
# ~/test-devmon4/recur.yaml
LoopOnly:
  on:
    - type: DeviceConnected
      options:
        device_bus: "loop"
      do:
        - shell: "echo 'LOOP CONNECTED: {{.DeviceName}} path={{.DevicePath}}' >> loop.results"
    - type: DeviceDisconnected
      options:
        device_bus: "loop"
      do:
        - shell: "echo 'LOOP DISCONNECTED: {{.DeviceName}} path={{.DevicePath}}' >> loop.results"
```

```sh
cd ~/test-devmon4 && recur start --foreground &
recur register

# One LOOP CONNECTED line per attach, one LOOP DISCONNECTED per detach.
# Works for both freshly-allocated and recycled loop nodes — the plugin
# tracks Loop.BackingFile transitions rather than relying on udev
# add/remove of the node itself.
truncate -s 64M /tmp/loop.img
LOOP=$(sudo losetup --show -f /tmp/loop.img)   # -> LOOP CONNECTED
sudo losetup -d "$LOOP"                        # -> LOOP DISCONNECTED
rm /tmp/loop.img

# USB stick: expect NO events (bus=usb, not loop).
```

## Test 5: Combined device_type + device_bus filters

Covers: AND-combined filter axes.

```yaml
# ~/test-devmon5/recur.yaml
USBPartitionsOnly:
  on:
    - type: DeviceConnected
      options:
        device_type: "block"
        device_bus: "usb"
      do:
        - shell: "echo 'USB PART: {{.DeviceName}} drive={{.DrivePath}}' >> usb_parts.results"
```

```sh
cd ~/test-devmon5 && recur start --foreground &
recur register

# Insert a USB drive -> expect one USB PART line per block device
# (sda + each partition); the drive event is filtered out (type != block).
# Insert a loop device -> NO events (bus != usb).
```

## What to verify

- [ ] DeviceConnected fires on device insertion (one event per UDisks2 interface)
- [ ] DeviceDisconnected fires on device removal
- [ ] Drive events have `DeviceType=drive` and empty `DrivePath`
- [ ] Block events have `DeviceType=block` and `DrivePath` set when there's a parent drive
- [ ] `DeviceBus=usb` on every event from a USB stick (drive + each partition)
- [ ] `DeviceBus=loop` on loop devices, with empty `DrivePath`
- [ ] `device_bus=usb` fires for ALL events on a USB stick (drive + blocks)
- [ ] `device_type=block` fires for both USB partitions AND loop devices (bus-agnostic)
- [ ] `device_type=drive` fires once per USB insert (the drive object only)
- [ ] `device_type=all` (default) fires for every device event
- [ ] Each trigger only fires its own `do:` block (no cross-firing in Test 5)
- [ ] Context variables (`DeviceName`, `DeviceType`, `DeviceBus`, `DevicePath`, `DrivePath`, `MountPoint`) populated correctly
- [ ] `MountPoint` is empty on the initial CONNECTED event (mount completes asynchronously after the InterfacesAdded signal)
- [ ] On disconnect: `DeviceBus` and `DrivePath` are replayed from the busCache, populated at add time
- [ ] Plugin shows up in `recur list plugins`
- [ ] `recur inspect plugin devicemonitor` shows both triggers with `device_bus` option
- [ ] State persists across daemon restarts (LastFired timestamp)
