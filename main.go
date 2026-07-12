//go:build linux || windows

// devicemonitor is an external trigger plugin that watches for USB/block device
// connect/disconnect events via UDisks2 (Linux) or WMI polling (Windows).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	sdk "github.com/directedbits/recur/pkg/plugin-sdk"
)

// pluginInput is the JSON payload read from stdin.
type pluginInput struct {
	TriggerType string         `json:"trigger_type"`
	Options     map[string]any `json:"options"`
	Config      map[string]any `json:"config"`
}

// parsedDeviceInput holds the validated fields extracted from a pluginInput.
type parsedDeviceInput struct {
	Input      *pluginInput
	DeviceType string
	DeviceBus  string
}

// parseInput reads stdin JSON and validates the devicemonitor trigger configuration.
func parseInput(r io.Reader) (*parsedDeviceInput, error) {
	var input pluginInput
	if err := json.NewDecoder(r).Decode(&input); err != nil {
		return nil, fmt.Errorf("reading stdin: %w", err)
	}

	if input.TriggerType != "DeviceConnected" && input.TriggerType != "DeviceDisconnected" {
		return nil, fmt.Errorf("unsupported trigger_type: %s", input.TriggerType)
	}

	deviceType := "all"
	if dt, ok := input.Options["device_type"].(string); ok && dt != "" {
		deviceType = dt
	}

	deviceBus := "all"
	if db, ok := input.Options["device_bus"].(string); ok && db != "" {
		deviceBus = db
	}

	return &parsedDeviceInput{
		Input:      &input,
		DeviceType: deviceType,
		DeviceBus:  deviceBus,
	}, nil
}

func main() {
	log.SetPrefix("devicemonitor: ")
	log.SetFlags(0)

	parsed, err := parseInput(os.Stdin)
	if err != nil {
		log.Fatal(err)
	}

	// Read required env vars
	socketPath := os.Getenv("RECUR_SOCKET")
	triggerID := os.Getenv("RECUR_TRIGGER_ID")
	if socketPath == "" || triggerID == "" {
		log.Fatal("RECUR_SOCKET and RECUR_TRIGGER_ID must be set")
	}

	triggerType := parsed.Input.TriggerType
	deviceType := parsed.DeviceType
	deviceBus := parsed.DeviceBus

	// Create platform-specific device subscriber
	subscriber, err := newSubscriber()
	if err != nil {
		log.Fatalf("creating device subscriber: %v", err)
	}
	defer subscriber.Close()

	// Subscribe to device events
	events, err := subscriber.Subscribe(deviceType, deviceBus)
	if err != nil {
		log.Fatalf("subscribing to device events: %v", err)
	}

	// Connect to daemon gRPC socket
	client, err := sdk.Connect(socketPath)
	if err != nil {
		log.Fatalf("connecting to daemon: %v", err)
	}
	defer func() { _ = client.Close() }()

	log.Printf("started: trigger_type=%s device_type=%s device_bus=%s trigger_id=%s", triggerType, deviceType, deviceBus, triggerID)

	// Set up signal handler for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	// Event loop
	for {
		select {
		case event, ok := <-events:
			if !ok {
				log.Print("event channel closed, exiting")
				return
			}

			// Only report events matching our trigger type
			if triggerType == "DeviceConnected" && !event.Added {
				continue
			}
			if triggerType == "DeviceDisconnected" && event.Added {
				continue
			}

			ctxVars := map[string]string{
				"DeviceName": event.DeviceName,
				"DeviceType": event.DeviceType,
				"DeviceBus":  event.DeviceBus,
				"DevicePath": event.DevicePath,
				"DrivePath":  event.DrivePath,
			}
			if event.Added {
				ctxVars["MountPoint"] = event.MountPoint
			}

			resp, err := client.Service.ReportTriggerEvent(context.Background(), &sdk.ReportTriggerEventRequest{
				TriggerId: triggerID,
				Context:   ctxVars,
			})
			if err != nil {
				log.Printf("reporting event: %v", err)
				continue
			}
			if !resp.Accepted {
				log.Printf("event rejected: %s", resp.Error)
				continue
			}

			log.Printf("event reported: %s %s (%s)", eventVerb(event.Added), event.DeviceName, event.DeviceType)

		case sig := <-sigCh:
			fmt.Fprintf(os.Stderr, "received %v, shutting down\n", sig)
			return
		}
	}
}

func eventVerb(added bool) string {
	if added {
		return "connected"
	}
	return "disconnected"
}
