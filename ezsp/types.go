package ezsp

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
)

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

// EmberInitialSecurityBitmask controls which security fields are set.
type EmberInitialSecurityBitmask uint16

const (
	// EmberTrustCenterGlobalLinkKey indicates the preconfigured key is a global link key.
	EmberTrustCenterGlobalLinkKey EmberInitialSecurityBitmask = 0x0004
	// EmberHaveTrustCenterEUI64 indicates the trust center EUI-64 is provided.
	EmberHaveTrustCenterEUI64 EmberInitialSecurityBitmask = 0x0040
	// EmberHavePreconfiguredKey indicates a preconfigured link key is provided.
	EmberHavePreconfiguredKey EmberInitialSecurityBitmask = 0x0100
	// EmberHaveNetworkKey indicates a network key is provided.
	EmberHaveNetworkKey EmberInitialSecurityBitmask = 0x0200
)

// EmberInitialSecurityState holds the security configuration for network formation.
type EmberInitialSecurityState struct {
	Bitmask                      EmberInitialSecurityBitmask
	PreconfiguredKey             [16]byte
	NetworkKey                   [16]byte
	NetworkKeySequenceNumber     uint8
	PreconfiguredTrustCenterEUI64 [8]byte
}

// EmberNetworkInitBitmask controls network initialization behavior (EZSP v9+).
type EmberNetworkInitBitmask uint16

const (
	// EmberNetworkInitNoOptions performs standard network initialization.
	EmberNetworkInitNoOptions EmberNetworkInitBitmask = 0x0000
)

// ZigbeeHALinkKey is the well-known "ZigBeeAlliance09" trust center link key
// used by Zigbee Home Automation networks.
var ZigbeeHALinkKey = [16]byte{
	0x5A, 0x69, 0x67, 0x42, 0x65, 0x65, 0x41, 0x6C,
	0x6C, 0x69, 0x61, 0x6E, 0x63, 0x65, 0x30, 0x39,
}

// EzspConfigID identifies a configuration value on the NCP.
// Use with GetConfigurationValue / SetConfigurationValue.
type EzspConfigID uint8

const (
	ConfigPacketBufferCount                  EzspConfigID = 0x01
	ConfigNeighborTableSize                  EzspConfigID = 0x02
	ConfigAPSUnicastMessageCount             EzspConfigID = 0x03
	ConfigBindingTableSize                   EzspConfigID = 0x04
	ConfigAddressTableSize                   EzspConfigID = 0x05
	ConfigMulticastTableSize                 EzspConfigID = 0x06
	ConfigRouteTableSize                     EzspConfigID = 0x07
	ConfigDiscoveryTableSize                 EzspConfigID = 0x08
	ConfigStackProfile                       EzspConfigID = 0x0C
	ConfigSecurityLevel                      EzspConfigID = 0x0D
	ConfigMaxHops                            EzspConfigID = 0x10
	ConfigMaxEndDeviceChildren               EzspConfigID = 0x11
	ConfigIndirectTransmissionTimeout        EzspConfigID = 0x12
	ConfigEndDevicePollTimeout               EzspConfigID = 0x13
	ConfigTXPowerMode                        EzspConfigID = 0x17
	ConfigDisableRelay                       EzspConfigID = 0x18
	ConfigTrustCenterAddressCacheSize        EzspConfigID = 0x19
	ConfigSourceRouteTableSize               EzspConfigID = 0x1A
	ConfigFragmentWindowSize                 EzspConfigID = 0x1C
	ConfigFragmentDelayMS                    EzspConfigID = 0x1D
	ConfigKeyTableSize                       EzspConfigID = 0x1E
	ConfigAPSACKTimeout                      EzspConfigID = 0x1F
	ConfigBeaconJitterDuration               EzspConfigID = 0x20
	ConfigEndDeviceBindTimeout               EzspConfigID = 0x21
	ConfigPanIDConflictReportThreshold       EzspConfigID = 0x22
	ConfigRequestKeyTimeout                  EzspConfigID = 0x24
	ConfigCertificateTableSize               EzspConfigID = 0x29
	ConfigApplicationZDOFlags                EzspConfigID = 0x2A
	ConfigBroadcastTableSize                 EzspConfigID = 0x2B
	ConfigMACFilterTableSize                 EzspConfigID = 0x2C
	ConfigSupportedNetworks                  EzspConfigID = 0x2D
	ConfigSendMulticastsToSleepyAddress      EzspConfigID = 0x2E
	ConfigZLLGroupAddresses                  EzspConfigID = 0x2F
	ConfigZLLRSSIThreshold                   EzspConfigID = 0x30
	ConfigMTORRFlowControl                   EzspConfigID = 0x33
	ConfigRetryQueueSize                     EzspConfigID = 0x34
	ConfigNewBroadcastEntryThreshold         EzspConfigID = 0x35
	ConfigTransientKeyTimeoutS               EzspConfigID = 0x36
	ConfigBroadcastMinACKsNeeded             EzspConfigID = 0x37
	ConfigTCRejoinsUsingWellKnownKeyTimeoutS EzspConfigID = 0x38
	ConfigCTuneValue                         EzspConfigID = 0x39
)

// Zigbee profile IDs.
const (
	// ProfileIDZDP is the Zigbee Device Profile (ZDO) profile ID.
	ProfileIDZDP uint16 = 0x0000
	// ProfileIDHA is the Zigbee Home Automation profile ID.
	ProfileIDHA uint16 = 0x0104
)

// Common APS options for unicast messaging.
const (
	// APSOptionRetry enables APS-layer retries.
	APSOptionRetry uint16 = 0x0040
	// APSOptionEnableRouteDiscovery enables route discovery for the message.
	APSOptionEnableRouteDiscovery uint16 = 0x0100
)

// EmberApsFrame represents an APS frame header used in Zigbee messaging.
// Wire format: profileId(2 LE) + clusterId(2 LE) + srcEndpoint(1) +
// dstEndpoint(1) + options(2 LE) + groupId(2 LE) + sequence(1) = 11 bytes.
type EmberApsFrame struct {
	ProfileID           uint16
	ClusterID           uint16
	SourceEndpoint      uint8
	DestinationEndpoint uint8
	Options             uint16
	GroupID             uint16
	Sequence            uint8
}

// ApsFrameSize is the wire size of an EmberApsFrame.
const ApsFrameSize = 11

// EncodeApsFrame encodes an EmberApsFrame to wire format (11 bytes).
func EncodeApsFrame(f EmberApsFrame) []byte {
	buf := make([]byte, ApsFrameSize)
	binary.LittleEndian.PutUint16(buf[0:2], f.ProfileID)
	binary.LittleEndian.PutUint16(buf[2:4], f.ClusterID)
	buf[4] = f.SourceEndpoint
	buf[5] = f.DestinationEndpoint
	binary.LittleEndian.PutUint16(buf[6:8], f.Options)
	binary.LittleEndian.PutUint16(buf[8:10], f.GroupID)
	buf[10] = f.Sequence
	return buf
}

// DecodeApsFrame decodes an EmberApsFrame from wire format.
func DecodeApsFrame(data []byte) (EmberApsFrame, error) {
	if len(data) < ApsFrameSize {
		return EmberApsFrame{}, fmt.Errorf("ezsp: APS frame too short (%d bytes)", len(data))
	}
	return EmberApsFrame{
		ProfileID:           binary.LittleEndian.Uint16(data[0:2]),
		ClusterID:           binary.LittleEndian.Uint16(data[2:4]),
		SourceEndpoint:      data[4],
		DestinationEndpoint: data[5],
		Options:             binary.LittleEndian.Uint16(data[6:8]),
		GroupID:             binary.LittleEndian.Uint16(data[8:10]),
		Sequence:            data[10],
	}, nil
}

// configNames maps each config ID to its human-readable name.
var configNames = map[EzspConfigID]string{
	ConfigPacketBufferCount:                  "PACKET_BUFFER_COUNT",
	ConfigNeighborTableSize:                  "NEIGHBOR_TABLE_SIZE",
	ConfigAPSUnicastMessageCount:             "APS_UNICAST_MESSAGE_COUNT",
	ConfigBindingTableSize:                   "BINDING_TABLE_SIZE",
	ConfigAddressTableSize:                   "ADDRESS_TABLE_SIZE",
	ConfigMulticastTableSize:                 "MULTICAST_TABLE_SIZE",
	ConfigRouteTableSize:                     "ROUTE_TABLE_SIZE",
	ConfigDiscoveryTableSize:                 "DISCOVERY_TABLE_SIZE",
	ConfigStackProfile:                       "STACK_PROFILE",
	ConfigSecurityLevel:                      "SECURITY_LEVEL",
	ConfigMaxHops:                            "MAX_HOPS",
	ConfigMaxEndDeviceChildren:               "MAX_END_DEVICE_CHILDREN",
	ConfigIndirectTransmissionTimeout:        "INDIRECT_TRANSMISSION_TIMEOUT",
	ConfigEndDevicePollTimeout:               "END_DEVICE_POLL_TIMEOUT",
	ConfigTXPowerMode:                        "TX_POWER_MODE",
	ConfigDisableRelay:                       "DISABLE_RELAY",
	ConfigTrustCenterAddressCacheSize:        "TRUST_CENTER_ADDRESS_CACHE_SIZE",
	ConfigSourceRouteTableSize:               "SOURCE_ROUTE_TABLE_SIZE",
	ConfigFragmentWindowSize:                 "FRAGMENT_WINDOW_SIZE",
	ConfigFragmentDelayMS:                    "FRAGMENT_DELAY_MS",
	ConfigKeyTableSize:                       "KEY_TABLE_SIZE",
	ConfigAPSACKTimeout:                      "APS_ACK_TIMEOUT",
	ConfigBeaconJitterDuration:               "BEACON_JITTER_DURATION",
	ConfigEndDeviceBindTimeout:               "END_DEVICE_BIND_TIMEOUT",
	ConfigPanIDConflictReportThreshold:       "PAN_ID_CONFLICT_REPORT_THRESHOLD",
	ConfigRequestKeyTimeout:                  "REQUEST_KEY_TIMEOUT",
	ConfigCertificateTableSize:               "CERTIFICATE_TABLE_SIZE",
	ConfigApplicationZDOFlags:                "APPLICATION_ZDO_FLAGS",
	ConfigBroadcastTableSize:                 "BROADCAST_TABLE_SIZE",
	ConfigMACFilterTableSize:                 "MAC_FILTER_TABLE_SIZE",
	ConfigSupportedNetworks:                  "SUPPORTED_NETWORKS",
	ConfigSendMulticastsToSleepyAddress:      "SEND_MULTICASTS_TO_SLEEPY_ADDRESS",
	ConfigZLLGroupAddresses:                  "ZLL_GROUP_ADDRESSES",
	ConfigZLLRSSIThreshold:                   "ZLL_RSSI_THRESHOLD",
	ConfigMTORRFlowControl:                   "MTORR_FLOW_CONTROL",
	ConfigRetryQueueSize:                     "RETRY_QUEUE_SIZE",
	ConfigNewBroadcastEntryThreshold:         "NEW_BROADCAST_ENTRY_THRESHOLD",
	ConfigTransientKeyTimeoutS:               "TRANSIENT_KEY_TIMEOUT_S",
	ConfigBroadcastMinACKsNeeded:             "BROADCAST_MIN_ACKS_NEEDED",
	ConfigTCRejoinsUsingWellKnownKeyTimeoutS: "TC_REJOINS_USING_WELL_KNOWN_KEY_TIMEOUT_S",
	ConfigCTuneValue:                         "CTUNE_VALUE",
}

// AllConfigIDs is the list of all known configuration IDs, ordered by value.
var AllConfigIDs = []EzspConfigID{
	ConfigPacketBufferCount,
	ConfigNeighborTableSize,
	ConfigAPSUnicastMessageCount,
	ConfigBindingTableSize,
	ConfigAddressTableSize,
	ConfigMulticastTableSize,
	ConfigRouteTableSize,
	ConfigDiscoveryTableSize,
	ConfigStackProfile,
	ConfigSecurityLevel,
	ConfigMaxHops,
	ConfigMaxEndDeviceChildren,
	ConfigIndirectTransmissionTimeout,
	ConfigEndDevicePollTimeout,
	ConfigTXPowerMode,
	ConfigDisableRelay,
	ConfigTrustCenterAddressCacheSize,
	ConfigSourceRouteTableSize,
	ConfigFragmentWindowSize,
	ConfigFragmentDelayMS,
	ConfigKeyTableSize,
	ConfigAPSACKTimeout,
	ConfigBeaconJitterDuration,
	ConfigEndDeviceBindTimeout,
	ConfigPanIDConflictReportThreshold,
	ConfigRequestKeyTimeout,
	ConfigCertificateTableSize,
	ConfigApplicationZDOFlags,
	ConfigBroadcastTableSize,
	ConfigMACFilterTableSize,
	ConfigSupportedNetworks,
	ConfigSendMulticastsToSleepyAddress,
	ConfigZLLGroupAddresses,
	ConfigZLLRSSIThreshold,
	ConfigMTORRFlowControl,
	ConfigRetryQueueSize,
	ConfigNewBroadcastEntryThreshold,
	ConfigTransientKeyTimeoutS,
	ConfigBroadcastMinACKsNeeded,
	ConfigTCRejoinsUsingWellKnownKeyTimeoutS,
	ConfigCTuneValue,
}

// String returns the human-readable name for the config ID.
func (id EzspConfigID) String() string {
	if name, ok := configNames[id]; ok {
		return name
	}
	return fmt.Sprintf("UNKNOWN_0x%02X", byte(id))
}

// ParseConfigID parses a config ID from a name (case-insensitive) or hex string (e.g. "0x01").
func ParseConfigID(s string) (EzspConfigID, error) {
	// Try hex format first.
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		v, err := strconv.ParseUint(s[2:], 16, 8)
		if err != nil {
			return 0, fmt.Errorf("invalid config ID hex %q: %w", s, err)
		}
		return EzspConfigID(v), nil
	}

	// Try decimal.
	if v, err := strconv.ParseUint(s, 10, 8); err == nil {
		return EzspConfigID(v), nil
	}

	// Try name lookup (case-insensitive).
	upper := strings.ToUpper(s)
	for id, name := range configNames {
		if name == upper {
			return id, nil
		}
	}

	return 0, fmt.Errorf("unknown config ID: %q", s)
}
