package sdk

import (
	"encoding/json"
	"fmt"
	"os"
)

type RuntimePolicy struct {
	StateMachine                 StateMachineConfig       `json:"state_machine"`
	CircuitBreaker               CircuitBreakerConfigFile `json:"circuit_breaker"`
	DNSRefreshShortSec           int                      `json:"dns_refresh_short_sec,omitempty"`
	DNSRefreshLongSec            int                      `json:"dns_refresh_long_sec,omitempty"`
	DNSPersistFailures           int                      `json:"dns_persist_failures,omitempty"`
	NetworkSwitchProtectSec      int                      `json:"network_switch_protect_sec,omitempty"`
	HighAvailabilityHeartbeatSec int                      `json:"high_availability_heartbeat_sec,omitempty"`
	RecentSuccessPriority        bool                     `json:"recent_success_priority,omitempty"`
}
type CircuitBreakerConfigFile struct {
	MaxFailures         int `json:"max_failures"`
	BaseBackoffMs       int `json:"base_backoff_ms"`
	MaxBackoffMs        int `json:"max_backoff_ms"`
	HalfOpenProbeMaxSec int `json:"half_open_probe_max_sec,omitempty"`
}

func loadRuntimePolicy(path string) (*RuntimePolicy, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read runtime policy: %w", err)
	}
	var p RuntimePolicy
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("parse runtime policy json: %w", err)
	}
	return &p, nil
}

