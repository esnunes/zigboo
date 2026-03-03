package ezsp

import "testing"

func TestEmberNetworkStatusString(t *testing.T) {
	tests := []struct {
		status EmberNetworkStatus
		want   string
	}{
		{NetworkStatusNoNetwork, "no network"},
		{NetworkStatusJoiningNetwork, "joining"},
		{NetworkStatusJoinedNetwork, "joined"},
		{NetworkStatusJoinedNoParent, "joined (no parent)"},
		{NetworkStatusLeavingNetwork, "leaving"},
		{EmberNetworkStatus(0xFF), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := tt.status.String()
			if got != tt.want {
				t.Errorf("EmberNetworkStatus(%d).String() = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

func TestEzspConfigID_String(t *testing.T) {
	tests := []struct {
		id   EzspConfigID
		want string
	}{
		{ConfigPacketBufferCount, "PACKET_BUFFER_COUNT"},
		{ConfigStackProfile, "STACK_PROFILE"},
		{ConfigMaxHops, "MAX_HOPS"},
		{ConfigCTuneValue, "CTUNE_VALUE"},
		{EzspConfigID(0xFE), "UNKNOWN_0xFE"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := tt.id.String()
			if got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseConfigID_ByName(t *testing.T) {
	id, err := ParseConfigID("PACKET_BUFFER_COUNT")
	if err != nil {
		t.Fatalf("ParseConfigID() error = %v", err)
	}
	if id != ConfigPacketBufferCount {
		t.Errorf("ParseConfigID() = 0x%02X, want 0x%02X", id, ConfigPacketBufferCount)
	}
}

func TestParseConfigID_ByHex(t *testing.T) {
	id, err := ParseConfigID("0x01")
	if err != nil {
		t.Fatalf("ParseConfigID() error = %v", err)
	}
	if id != ConfigPacketBufferCount {
		t.Errorf("ParseConfigID() = 0x%02X, want 0x%02X", id, ConfigPacketBufferCount)
	}
}

func TestParseConfigID_CaseInsensitive(t *testing.T) {
	id, err := ParseConfigID("packet_buffer_count")
	if err != nil {
		t.Fatalf("ParseConfigID() error = %v", err)
	}
	if id != ConfigPacketBufferCount {
		t.Errorf("ParseConfigID() = 0x%02X, want 0x%02X", id, ConfigPacketBufferCount)
	}
}

func TestParseConfigID_Unknown(t *testing.T) {
	_, err := ParseConfigID("BOGUS")
	if err == nil {
		t.Fatal("expected error for unknown config ID")
	}
}

func TestEmberNodeTypeString(t *testing.T) {
	tests := []struct {
		nodeType EmberNodeType
		want     string
	}{
		{NodeTypeUnknown, "unknown"},
		{NodeTypeCoordinator, "coordinator"},
		{NodeTypeRouter, "router"},
		{NodeTypeEndDevice, "end device"},
		{NodeTypeSleepyEndDevice, "sleepy end device"},
		{EmberNodeType(0xFF), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := tt.nodeType.String()
			if got != tt.want {
				t.Errorf("EmberNodeType(%d).String() = %q, want %q", tt.nodeType, got, tt.want)
			}
		})
	}
}
