package sdk

import (
	"encoding/json"
	"fmt"
	"os"
)

type RuntimePolicy struct {
	StateMachine   StateMachineConfig       `json:"state_machine"`
	CircuitBreaker CircuitBreakerConfigFile `json:"circuit_breaker"`
}
type CircuitBreakerConfigFile struct {
	MaxFailures   int `json:"max_failures"`
	BaseBackoffMs int `json:"base_backoff_ms"`
	MaxBackoffMs  int `json:"max_backoff_ms"`
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

