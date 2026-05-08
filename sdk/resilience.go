package sdk

import (
	"hash/fnv"
	"math/rand"
	"sync"
	"time"
)

type SDKState string

const (
	StateNormal     SDKState = "NORMAL"
	StateDegraded   SDKState = "DEGRADED"
	StateAttack     SDKState = "ATTACK"
	StateRecovering SDKState = "RECOVERING"
	StateEmergency  SDKState = "EMERGENCY"
)

type StateMachineConfig struct {
	NormalToDegradedFail      int
	DegradedToAttackFail      int
	DegradedToNormalSuccess   int
	AttackToRecoverSuccess    int
	RecoverToAttackFail       int
	RecoverToNormalSuccess    int
	EmergencyToRecoverSuccess int
}

type StateMachine struct {
	mu              sync.Mutex
	state           SDKState
	consecFailures  int
	consecSuccesses int
	lastChangedAt   time.Time
	cfg             StateMachineConfig
}

func defaultStateMachineConfig() StateMachineConfig {
	return StateMachineConfig{2, 4, 3, 5, 2, 5, 2}
}

func normalizeStateMachineConfig(cfg *StateMachineConfig) {
	def := defaultStateMachineConfig()
	if cfg.NormalToDegradedFail <= 0 {
		cfg.NormalToDegradedFail = def.NormalToDegradedFail
	}
	if cfg.DegradedToAttackFail <= 0 {
		cfg.DegradedToAttackFail = def.DegradedToAttackFail
	}
	if cfg.DegradedToNormalSuccess <= 0 {
		cfg.DegradedToNormalSuccess = def.DegradedToNormalSuccess
	}
	if cfg.AttackToRecoverSuccess <= 0 {
		cfg.AttackToRecoverSuccess = def.AttackToRecoverSuccess
	}
	if cfg.RecoverToAttackFail <= 0 {
		cfg.RecoverToAttackFail = def.RecoverToAttackFail
	}
	if cfg.RecoverToNormalSuccess <= 0 {
		cfg.RecoverToNormalSuccess = def.RecoverToNormalSuccess
	}
	if cfg.EmergencyToRecoverSuccess <= 0 {
		cfg.EmergencyToRecoverSuccess = def.EmergencyToRecoverSuccess
	}
}

func NewStateMachine() *StateMachine { return NewStateMachineWithConfig(defaultStateMachineConfig()) }
func NewStateMachineWithConfig(cfg StateMachineConfig) *StateMachine {
	normalizeStateMachineConfig(&cfg)
	return &StateMachine{state: StateNormal, lastChangedAt: time.Now(), cfg: cfg}
}

func (s *StateMachine) State() SDKState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *StateMachine) RecordResult(success bool) SDKState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if success {
		s.consecSuccesses++
		s.consecFailures = 0
	} else {
		s.consecFailures++
		s.consecSuccesses = 0
	}
	prev := s.state
	switch s.state {
	case StateNormal:
		if s.consecFailures >= s.cfg.NormalToDegradedFail {
			s.state = StateDegraded
		}
	case StateDegraded:
		if s.consecFailures >= s.cfg.DegradedToAttackFail {
			s.state = StateAttack
		} else if s.consecSuccesses >= s.cfg.DegradedToNormalSuccess {
			s.state = StateNormal
		}
	case StateAttack:
		if s.consecSuccesses >= s.cfg.AttackToRecoverSuccess {
			s.state = StateRecovering
		}
	case StateRecovering:
		if s.consecFailures >= s.cfg.RecoverToAttackFail {
			s.state = StateAttack
		} else if s.consecSuccesses >= s.cfg.RecoverToNormalSuccess {
			s.state = StateNormal
		}
	case StateEmergency:
		if s.consecSuccesses >= s.cfg.EmergencyToRecoverSuccess {
			s.state = StateRecovering
		}
	}
	if prev != s.state {
		s.lastChangedAt = time.Now()
	}
	return s.state
}

func (s *StateMachine) EnterEmergency() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = StateEmergency
	s.consecFailures, s.consecSuccesses = 0, 0
	s.lastChangedAt = time.Now()
}

type CircuitBreakerConfig struct {
	MaxFailures int
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
}
type CircuitBreaker struct {
	mu          sync.Mutex
	failures    map[string]int
	openUntil   map[string]time.Time
	maxFailures int
	baseBackoff time.Duration
	maxBackoff  time.Duration
}

func defaultCircuitBreakerConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{MaxFailures: 3, BaseBackoff: time.Second, MaxBackoff: 60 * time.Second}
}
func NewCircuitBreaker() *CircuitBreaker { return NewCircuitBreakerWithConfig(defaultCircuitBreakerConfig()) }
func NewCircuitBreakerWithConfig(cfg CircuitBreakerConfig) *CircuitBreaker {
	def := defaultCircuitBreakerConfig()
	if cfg.MaxFailures <= 0 {
		cfg.MaxFailures = def.MaxFailures
	}
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = def.BaseBackoff
	}
	if cfg.MaxBackoff < cfg.BaseBackoff {
		cfg.MaxBackoff = def.MaxBackoff
	}
	return &CircuitBreaker{
		failures:    map[string]int{},
		openUntil:   map[string]time.Time{},
		maxFailures: cfg.MaxFailures,
		baseBackoff: cfg.BaseBackoff,
		maxBackoff:  cfg.MaxBackoff,
	}
}
func (c *CircuitBreaker) Allow(node string, now time.Time) bool {
	c.mu.Lock(); defer c.mu.Unlock()
	u := c.openUntil[node]
	return u.IsZero() || now.After(u)
}
func (c *CircuitBreaker) RecordSuccess(node string) {
	c.mu.Lock(); defer c.mu.Unlock()
	c.failures[node] = 0
	delete(c.openUntil, node)
}
func (c *CircuitBreaker) RecordFailure(node string, now time.Time) {
	c.mu.Lock(); defer c.mu.Unlock()
	c.failures[node]++
	if c.failures[node] < c.maxFailures {
		return
	}
	d := expBackoff(c.failures[node]-c.maxFailures, c.baseBackoff, c.maxBackoff)
	c.openUntil[node] = now.Add(withJitter(d, node))
}
func (c *CircuitBreaker) FailureCount(node string) int {
	c.mu.Lock(); defer c.mu.Unlock()
	return c.failures[node]
}
func (c *CircuitBreaker) OpenUntil(node string) time.Time {
	c.mu.Lock(); defer c.mu.Unlock()
	return c.openUntil[node]
}

func expBackoff(step int, base, max time.Duration) time.Duration {
	if step < 0 {
		return base
	}
	d := base
	for i := 0; i < step; i++ {
		d *= 2
		if d >= max {
			return max
		}
	}
	if d > max {
		return max
	}
	return d
}

func withJitter(d time.Duration, key string) time.Duration {
	if d <= 0 {
		return d
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	r := rand.New(rand.NewSource(int64(h.Sum32()) + time.Now().UnixNano()))
	return time.Duration(float64(d) * (0.8 + r.Float64()*0.4))
}

