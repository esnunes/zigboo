package zcl

import "encoding/binary"

// ZCL command IDs.
const (
	cmdReadAttributes         = 0x00
	cmdReadAttributesResponse = 0x01
)

// ZCL frame control bits.
const (
	// fcDisableDefaultResponse sets bit 4 to suppress Default Response commands.
	fcDisableDefaultResponse = 0x10
)

// encodeReadAttributes builds a ZCL Read Attributes frame.
// Frame: FC(1) + seq(1) + cmdID(1) + attrIDs(N*2 LE).
func encodeReadAttributes(seq uint8, attrIDs []uint16) []byte {
	frame := make([]byte, 3+len(attrIDs)*2)
	frame[0] = fcDisableDefaultResponse // global, client→server
	frame[1] = seq
	frame[2] = cmdReadAttributes
	for i, id := range attrIDs {
		binary.LittleEndian.PutUint16(frame[3+i*2:], id)
	}
	return frame
}

// decodeReadAttributesResponse parses a ZCL Read Attributes Response payload.
// Returns attribute values keyed by attribute ID.
func decodeReadAttributesResponse(data []byte) (map[uint16]AttributeValue, error) {
	if len(data) < 3 {
		return nil, errFrameTooShort
	}
	// Skip FC(1) + seq(1) + cmdID(1).
	records := data[3:]
	result := make(map[uint16]AttributeValue)

	for len(records) >= 3 {
		attrID := binary.LittleEndian.Uint16(records[0:2])
		status := records[2]
		records = records[3:]

		if status != 0x00 {
			result[attrID] = AttributeValue{Status: status}
			continue
		}

		if len(records) < 1 {
			break
		}
		dataType := records[0]
		records = records[1:]

		value, n := decodeValue(dataType, records)
		records = records[n:]
		result[attrID] = AttributeValue{
			Status:   0,
			DataType: dataType,
			Value:    value,
		}
	}

	return result, nil
}

// decodeValue decodes a ZCL value from the given data type.
// Returns the decoded value and the number of bytes consumed.
func decodeValue(dataType byte, data []byte) (any, int) {
	switch dataType {
	case DataTypeCharString: // 0x42
		if len(data) < 1 {
			return nil, 0
		}
		strLen := int(data[0])
		if strLen == 0xFF { // invalid/null string
			return "", 1
		}
		if len(data) < 1+strLen {
			return string(data[1:]), len(data)
		}
		return string(data[1 : 1+strLen]), 1 + strLen

	default:
		// Unknown type — cannot determine length, stop parsing.
		return nil, 0
	}
}
