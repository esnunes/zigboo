package host

import (
	"encoding/binary"
	"fmt"

	"github.com/esnunes/zigboo/ezsp"
)

// IncomingMessage represents a decoded incomingMessageHandler callback.
type IncomingMessage struct {
	Type     byte
	ApsFrame ezsp.EmberApsFrame
	LQI      uint8
	RSSI     int8
	SenderID uint16
	Payload  []byte
}

// messageHandlerKey identifies a message handler by profile and cluster ID.
type messageHandlerKey struct {
	profileID uint16
	clusterID uint16
}

// decodeIncomingMessage decodes the params from an incomingMessageHandler callback.
//
// Wire format: type(1) + apsFrame(11) + lastHopLqi(1) + lastHopRssi(1) +
// sender(2) + bindingIndex(1) + addressIndex(1) + messageLength(1) + message(N).
func decodeIncomingMessage(params []byte) (IncomingMessage, error) {
	const minLen = 1 + ezsp.ApsFrameSize + 1 + 1 + 2 + 1 + 1 + 1 // 19
	if len(params) < minLen {
		return IncomingMessage{}, fmt.Errorf("host: incomingMessage too short (%d bytes)", len(params))
	}

	msg := IncomingMessage{
		Type: params[0],
	}

	aps, err := ezsp.DecodeApsFrame(params[1:12])
	if err != nil {
		return IncomingMessage{}, err
	}
	msg.ApsFrame = aps

	msg.LQI = params[12]
	msg.RSSI = int8(params[13])
	msg.SenderID = binary.LittleEndian.Uint16(params[14:16])
	// bindingIndex = params[16], addressIndex = params[17] — unused
	msgLen := int(params[18])
	if len(params) < minLen+msgLen {
		return IncomingMessage{}, fmt.Errorf("host: incomingMessage payload truncated (have %d, need %d)", len(params)-minLen, msgLen)
	}
	msg.Payload = params[19 : 19+msgLen]

	return msg, nil
}
