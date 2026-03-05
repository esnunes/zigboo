package zdo

import "encoding/binary"

// NodeDescriptor holds the parsed ZDO Node Descriptor.
type NodeDescriptor struct {
	LogicalType         uint8  // 0=coordinator, 1=router, 2=end device
	MACCapabilities     uint8  // raw MAC capability flags
	ManufacturerCode    uint16
	MaxBufferSize       uint8
	MaxIncomingTransfer uint16
	MaxOutgoingTransfer uint16
	ServerMask          uint16
}

// parseNodeDescriptor parses a 13-byte ZDO node descriptor.
func parseNodeDescriptor(data []byte) (NodeDescriptor, error) {
	if len(data) < 13 {
		return NodeDescriptor{}, errResponseTooShort
	}
	return NodeDescriptor{
		LogicalType:         data[0] & 0x07,
		MACCapabilities:     data[2],
		ManufacturerCode:    binary.LittleEndian.Uint16(data[3:5]),
		MaxBufferSize:       data[5],
		MaxIncomingTransfer: binary.LittleEndian.Uint16(data[6:8]),
		ServerMask:          binary.LittleEndian.Uint16(data[8:10]),
		MaxOutgoingTransfer: binary.LittleEndian.Uint16(data[10:12]),
	}, nil
}

// SimpleDescriptor holds the parsed ZDO Simple Descriptor.
type SimpleDescriptor struct {
	Endpoint       uint8
	ProfileID      uint16
	DeviceID       uint16
	DeviceVersion  uint8
	InputClusters  []uint16
	OutputClusters []uint16
}

// parseSimpleDescriptor parses a ZDO simple descriptor from raw bytes.
func parseSimpleDescriptor(data []byte) (SimpleDescriptor, error) {
	// Minimum: endpoint(1) + profileId(2) + deviceId(2) + deviceVersion(1) + inCount(1) = 7
	if len(data) < 7 {
		return SimpleDescriptor{}, errResponseTooShort
	}

	sd := SimpleDescriptor{
		Endpoint:      data[0],
		ProfileID:     binary.LittleEndian.Uint16(data[1:3]),
		DeviceID:      binary.LittleEndian.Uint16(data[3:5]),
		DeviceVersion: data[5] & 0x0F,
	}

	off := 6
	inCount := int(data[off])
	off++
	if len(data) < off+inCount*2+1 {
		return SimpleDescriptor{}, errResponseTooShort
	}
	sd.InputClusters = make([]uint16, inCount)
	for i := range inCount {
		sd.InputClusters[i] = binary.LittleEndian.Uint16(data[off : off+2])
		off += 2
	}

	outCount := int(data[off])
	off++
	if len(data) < off+outCount*2 {
		return SimpleDescriptor{}, errResponseTooShort
	}
	sd.OutputClusters = make([]uint16, outCount)
	for i := range outCount {
		sd.OutputClusters[i] = binary.LittleEndian.Uint16(data[off : off+2])
		off += 2
	}

	return sd, nil
}
