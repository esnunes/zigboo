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
