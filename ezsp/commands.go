package ezsp

// EZSP command frame IDs.
const (
	// frameIDVersion is the EZSP version negotiation command (0x00).
	frameIDVersion = 0x0000

	// frameIDGetEUI64 returns the dongle's IEEE 802.15.4 address (0x0026).
	frameIDGetEUI64 = 0x0026

	// frameIDGetNodeID returns the dongle's short network address (0x0027).
	frameIDGetNodeID = 0x0027
)
