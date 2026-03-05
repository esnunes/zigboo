package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Device represents a paired Zigbee device.
type Device struct {
	IEEE              string           `json:"ieee"`
	NwkAddr           string           `json:"nwkAddr"`
	NodeType          string           `json:"nodeType"`
	Manufacturer      string           `json:"manufacturer,omitempty"`
	Model             string           `json:"model,omitempty"`
	Firmware          string           `json:"firmware,omitempty"`
	InterviewComplete bool             `json:"interviewComplete"`
	LastSeen          time.Time        `json:"lastSeen"`
	Endpoints         []DeviceEndpoint `json:"endpoints,omitempty"`
}

// DeviceEndpoint describes one endpoint on a device.
type DeviceEndpoint struct {
	ID          uint8    `json:"id"`
	ProfileID   string   `json:"profileId"`
	DeviceID    string   `json:"deviceId"`
	InClusters  []string `json:"inClusters"`
	OutClusters []string `json:"outClusters"`
}

// DeviceStore persists devices to a JSON file keyed by EUI-64.
type DeviceStore struct {
	path    string
	devices map[string]Device
}

// NewDeviceStore creates a store backed by the given file path.
func NewDeviceStore(path string) *DeviceStore {
	return &DeviceStore{
		path:    path,
		devices: make(map[string]Device),
	}
}

// Load reads devices from the JSON file. Missing file is not an error.
func (s *DeviceStore) Load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("device store: load: %w", err)
	}
	return json.Unmarshal(data, &s.devices)
}

// Save updates the device in memory and writes the entire store atomically.
func (s *DeviceStore) Save(dev Device) error {
	s.devices[dev.IEEE] = dev

	data, err := json.MarshalIndent(s.devices, "", "  ")
	if err != nil {
		return fmt.Errorf("device store: marshal: %w", err)
	}

	// Atomic write: temp file + rename.
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".devices-*.json")
	if err != nil {
		return fmt.Errorf("device store: create temp: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("device store: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("device store: close temp: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("device store: rename: %w", err)
	}
	return nil
}
