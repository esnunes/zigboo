package ezsp

// EmberNetworkStatus represents the NCP's current network state.
type EmberNetworkStatus byte

const (
	// NetworkStatusNoNetwork indicates the NCP is not joined to any network.
	NetworkStatusNoNetwork EmberNetworkStatus = 0x00
	// NetworkStatusJoiningNetwork indicates the NCP is in the process of joining.
	NetworkStatusJoiningNetwork EmberNetworkStatus = 0x01
	// NetworkStatusJoinedNetwork indicates the NCP is joined to a network.
	NetworkStatusJoinedNetwork EmberNetworkStatus = 0x02
	// NetworkStatusJoinedNoParent indicates the NCP is joined but has lost its parent.
	NetworkStatusJoinedNoParent EmberNetworkStatus = 0x03
	// NetworkStatusLeavingNetwork indicates the NCP is in the process of leaving.
	NetworkStatusLeavingNetwork EmberNetworkStatus = 0x04
)

// String returns a human-readable name for the network status.
func (s EmberNetworkStatus) String() string {
	switch s {
	case NetworkStatusNoNetwork:
		return "no network"
	case NetworkStatusJoiningNetwork:
		return "joining"
	case NetworkStatusJoinedNetwork:
		return "joined"
	case NetworkStatusJoinedNoParent:
		return "joined (no parent)"
	case NetworkStatusLeavingNetwork:
		return "leaving"
	default:
		return "unknown"
	}
}

// EmberNodeType represents the type of a node in the network.
type EmberNodeType byte

const (
	// NodeTypeUnknown indicates an unknown node type.
	NodeTypeUnknown EmberNodeType = 0x00
	// NodeTypeCoordinator indicates the node is a coordinator.
	NodeTypeCoordinator EmberNodeType = 0x01
	// NodeTypeRouter indicates the node is a router.
	NodeTypeRouter EmberNodeType = 0x02
	// NodeTypeEndDevice indicates the node is an end device.
	NodeTypeEndDevice EmberNodeType = 0x03
	// NodeTypeSleepyEndDevice indicates the node is a sleepy end device.
	NodeTypeSleepyEndDevice EmberNodeType = 0x04
)

// String returns a human-readable name for the node type.
func (t EmberNodeType) String() string {
	switch t {
	case NodeTypeUnknown:
		return "unknown"
	case NodeTypeCoordinator:
		return "coordinator"
	case NodeTypeRouter:
		return "router"
	case NodeTypeEndDevice:
		return "end device"
	case NodeTypeSleepyEndDevice:
		return "sleepy end device"
	default:
		return "unknown"
	}
}

// EzspNetworkScanType identifies the type of network scan.
type EzspNetworkScanType byte

const (
	// ScanTypeEnergy scans for RF energy levels on each channel.
	ScanTypeEnergy EzspNetworkScanType = 0x00
	// ScanTypeActive scans for existing Zigbee networks.
	ScanTypeActive EzspNetworkScanType = 0x01
)

// DefaultChannelMask covers Zigbee 2.4 GHz channels 11-26.
const DefaultChannelMask = 0x07FFF800

// NetworkParameters holds the network configuration from getNetworkParameters.
type NetworkParameters struct {
	ExtendedPanID [8]byte
	PanID         uint16
	RadioTxPower  int8
	RadioChannel  uint8
}

// EnergyScanResult holds the result of an energy scan for a single channel.
type EnergyScanResult struct {
	Channel uint8
	MaxRSSI int8
}

// NetworkScanResult holds the result of an active scan for a discovered network.
type NetworkScanResult struct {
	Channel       uint8
	PanID         uint16
	ExtendedPanID [8]byte
	AllowingJoin  bool
	StackProfile  uint8
	NwkUpdateID   uint8
	LQI           uint8
	RSSI          int8
}

// EndpointDescription holds the description of a registered endpoint.
type EndpointDescription struct {
	ProfileID          uint16
	DeviceID           uint16
	DeviceVersion      uint8
	InputClusterCount  uint8
	OutputClusterCount uint8
}
