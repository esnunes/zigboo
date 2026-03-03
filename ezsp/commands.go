package ezsp

// EmberStatus values used by scan completion.
const (
	// emberSuccess indicates the operation completed successfully.
	emberSuccess = 0x00

	// emberNoBeacons indicates an active scan completed with no beacons found
	// on the last channel. This is the normal completion status for active scans.
	emberNoBeacons = 0x36
)

// EZSP command frame IDs.
const (
	// frameIDVersion is the EZSP version negotiation command (0x0000).
	frameIDVersion = 0x0000

	// frameIDNetworkState queries the current network state (0x0018).
	frameIDNetworkState = 0x0018

	// frameIDStartScan initiates an energy or active scan (0x001A).
	frameIDStartScan = 0x001A

	// frameIDNetworkFoundHandler is the callback for active scan results (0x001B).
	frameIDNetworkFoundHandler = 0x001B

	// frameIDScanCompleteHandler is the callback when a scan finishes (0x001C).
	frameIDScanCompleteHandler = 0x001C

	// frameIDGetEUI64 returns the dongle's IEEE 802.15.4 address (0x0026).
	frameIDGetEUI64 = 0x0026

	// frameIDGetNodeID returns the dongle's short network address (0x0027).
	frameIDGetNodeID = 0x0027

	// frameIDGetNetworkParameters reads current network parameters (0x0028).
	frameIDGetNetworkParameters = 0x0028

	// frameIDEnergyScanResultHandler is the callback for energy scan results (0x0048).
	frameIDEnergyScanResultHandler = 0x0048

	// frameIDGetConfigurationValue reads a configuration value (0x0052).
	frameIDGetConfigurationValue = 0x0052

	// frameIDSetConfigurationValue writes a configuration value (0x0053).
	frameIDSetConfigurationValue = 0x0053

	// frameIDGetEndpoint returns the endpoint number at a given index (0x012E).
	frameIDGetEndpoint = 0x012E

	// frameIDGetEndpointCount returns the number of configured endpoints (0x012F).
	frameIDGetEndpointCount = 0x012F

	// frameIDGetEndpointDescription returns the description of an endpoint (0x0130).
	frameIDGetEndpointDescription = 0x0130

	// frameIDGetEndpointCluster returns a cluster ID from an endpoint's cluster list (0x0131).
	frameIDGetEndpointCluster = 0x0131
)
