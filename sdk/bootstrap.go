package sdk

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

type SDKBootstrap struct {
	mu sync.Mutex

	secretToken string
	payload     *SecretPayload
	client      *ProxyClient
	state       *StateMachine
	breaker     *CircuitBreaker
	deviceUUID  string
	fixedSet    FixedNodeSet
	candidates  []string
	currentNode string
	cachePath   string
	nextDNSAt   time.Time

	lastFallbackReason string
	prepared           bool
	lastError          string
	updatedAt          time.Time
}

func NewSDKBootstrap() *SDKBootstrap {
	return &SDKBootstrap{
		client:    NewDefaultProxyClient(),
		state:     NewStateMachine(),
		breaker:   NewCircuitBreaker(),
		cachePath: "sdk_cache.json",
		updatedAt: time.Now(),
	}
}

func (b *SDKBootstrap) ConfigureStateMachine(cfg StateMachineConfig) {
	b.mu.Lock(); defer b.mu.Unlock()
	b.state = NewStateMachineWithConfig(cfg)
	b.updatedAt = time.Now()
}
func (b *SDKBootstrap) ConfigureCircuitBreaker(cfg CircuitBreakerConfig) {
	b.mu.Lock(); defer b.mu.Unlock()
	b.breaker = NewCircuitBreakerWithConfig(cfg)
	b.updatedAt = time.Now()
}
func (b *SDKBootstrap) LoadRuntimePolicyFile(path string) error {
	p, err := loadRuntimePolicy(path)
	if err != nil {
		return b.fail(fmt.Sprintf("load runtime policy failed: %v", err))
	}
	b.ConfigureStateMachine(p.StateMachine)
	b.ConfigureCircuitBreaker(CircuitBreakerConfig{
		MaxFailures: p.CircuitBreaker.MaxFailures,
		BaseBackoff: time.Duration(p.CircuitBreaker.BaseBackoffMs) * time.Millisecond,
		MaxBackoff:  time.Duration(p.CircuitBreaker.MaxBackoffMs) * time.Millisecond,
	})
	return nil
}
func (b *SDKBootstrap) LoadConfigFile(path string) error {
	b.mu.Lock(); defer b.mu.Unlock()
	if err := LoadEmbeddedPrivateKeyFromConfigFile(path); err != nil {
		return b.failLocked(fmt.Sprintf("load config file failed: %v", err))
	}
	b.lastError = ""
	b.updatedAt = time.Now()
	return nil
}
func (b *SDKBootstrap) Init(secret string) error {
	b.mu.Lock(); defer b.mu.Unlock()
	if secret == "" {
		return b.failLocked("empty secret")
	}
	payload, err := DecodeSecretPayload(secret)
	if err != nil {
		return b.failLocked(fmt.Sprintf("decode secret failed: %v", err))
	}
	b.secretToken, b.payload = secret, payload
	if b.deviceUUID == "" {
		b.deviceUUID = newDeviceUUID()
	}
	b.prepared, b.lastError, b.updatedAt = false, "", time.Now()
	return nil
}
func (b *SDKBootstrap) Prepare() error {
	b.mu.Lock()
	if b.payload == nil {
		defer b.mu.Unlock()
		return b.failLocked("init must be called before prepare")
	}
	if b.payload.AppName == "" || b.payload.SDKVersion == "" || b.payload.AESKey == "" {
		defer b.mu.Unlock()
		return b.failLocked("invalid payload: missing required fields")
	}
	payloadCopy := *b.payload
	deviceUUID := b.deviceUUID
	b.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	cacheData, _ := loadSDKCache(b.cachePath)
	groups, updatedCache, err := resolveControlPlane(ctx, &payloadCopy, cacheData)
	if err != nil {
		return b.fail(fmt.Sprintf("resolve control plane failed: %v", err))
	}
	_ = saveSDKCache(b.cachePath, updatedCache)
	set := SelectFixedNodeSet(deviceUUID, groups)
	nodes := set.AsList()
	if len(nodes) == 0 {
		return b.fail("no usable fixed node in resolved groups")
	}
	primary := nodes[0]
	host, port, err := parseEndpoint(primary)
	if err != nil {
		return b.fail(fmt.Sprintf("invalid primary endpoint: %v", err))
	}

	b.mu.Lock()
	b.fixedSet, b.currentNode = set, primary
	b.candidates = StableFallbackOrder(deviceUUID, set, primary)
	b.client.config.ServerHost, b.client.config.ServerPort = host, port
	b.prepared, b.lastError, b.updatedAt = true, "", time.Now()
	b.mu.Unlock()
	return nil
}

func (b *SDKBootstrap) SetServerEndpoint(host string, port int) error {
	b.mu.Lock(); defer b.mu.Unlock()
	if host == "" || port <= 0 {
		return b.failLocked("invalid server endpoint")
	}
	b.client.config.ServerHost, b.client.config.ServerPort = host, port
	b.updatedAt = time.Now()
	return nil
}
func (b *SDKBootstrap) SetLocalPort(port int) error {
	b.mu.Lock(); defer b.mu.Unlock()
	if port < 0 || port > 65535 {
		return b.failLocked("invalid local port")
	}
	b.client.config.LocalPort = port
	b.updatedAt = time.Now()
	return nil
}
func (b *SDKBootstrap) Start() error {
	b.mu.Lock()
	client := b.client
	if !b.prepared {
		b.mu.Unlock()
		return b.fail("prepare must be called before start")
	}
	b.mu.Unlock()
	if err := client.Start(); err != nil {
		b.state.RecordResult(false)
		return b.fail(fmt.Sprintf("start failed: %v", err))
	}
	b.mu.Lock()
	b.state.RecordResult(true)
	b.lastError, b.updatedAt = "", time.Now()
	b.mu.Unlock()
	return nil
}
func (b *SDKBootstrap) Stop() { b.mu.Lock(); c := b.client; b.mu.Unlock(); c.Stop(); b.mu.Lock(); b.updatedAt = time.Now(); b.mu.Unlock() }
func (b *SDKBootstrap) LocalPort() int {
	b.mu.Lock(); c := b.client; b.mu.Unlock()
	return c.LocalPort()
}
func (b *SDKBootstrap) IsRunning() bool {
	b.mu.Lock(); c := b.client; b.mu.Unlock()
	return c.IsRunning()
}

func (b *SDKBootstrap) ReportConnectResult(node string, success bool) {
	b.mu.Lock(); defer b.mu.Unlock()
	if success {
		b.breaker.RecordSuccess(node)
	} else {
		b.breaker.RecordFailure(node, time.Now())
		if node == b.currentNode {
			for _, n := range b.candidates {
				if !b.breaker.Allow(n, time.Now()) {
					continue
				}
				host, port, err := parseEndpoint(n)
				if err != nil {
					continue
				}
				b.client.config.ServerHost, b.client.config.ServerPort = host, port
				b.currentNode, b.candidates = n, StableFallbackOrder(b.deviceUUID, b.fixedSet, n)
				b.lastFallbackReason = "fixed_set_switch"
				break
			}
			if node == b.currentNode {
				b.tryDomainFallbackLocked()
			}
		}
	}
	b.state.RecordResult(success)
	b.updatedAt = time.Now()
}
func (b *SDKBootstrap) CanTryNode(node string) bool {
	b.mu.Lock(); defer b.mu.Unlock()
	return b.breaker.Allow(node, time.Now())
}
func (b *SDKBootstrap) Status() string {
	b.mu.Lock(); defer b.mu.Unlock()
	type status struct {
		Prepared bool `json:"prepared"`; Running bool `json:"running"`; LocalPort int `json:"local_port"`
		ServerHost string `json:"server_host"`; ServerPort int `json:"server_port"`
		LastError string `json:"last_error,omitempty"`; Payload *SecretPayload `json:"payload,omitempty"`
		State SDKState `json:"state"`; DeviceUUID string `json:"device_uuid,omitempty"`
		FixedSet FixedNodeSet `json:"fixed_set"`; Current string `json:"current_node,omitempty"`
		Candidates []string `json:"candidates,omitempty"`; NextDNSAt int64 `json:"next_dns_refresh_unix,omitempty"`
		LastFallbackReason string `json:"last_fallback_reason,omitempty"`
		CurrentNodeFailureCount int `json:"current_node_failures"`
		CurrentNodeOpenUntil int64 `json:"current_node_open_until_unix,omitempty"`
		UpdatedAt int64 `json:"updated_at_unix"`
	}
	failCnt, openUntil := 0, int64(0)
	if b.currentNode != "" {
		failCnt = b.breaker.FailureCount(b.currentNode)
		openUntil = b.breaker.OpenUntil(b.currentNode).Unix()
	}
	out, err := json.Marshal(status{
		Prepared: b.prepared, Running: b.client.IsRunning(), LocalPort: b.client.LocalPort(),
		ServerHost: b.client.config.ServerHost, ServerPort: b.client.config.ServerPort,
		LastError: b.lastError, Payload: b.payload, State: b.state.State(), DeviceUUID: b.deviceUUID,
		FixedSet: b.fixedSet, Current: b.currentNode, Candidates: b.candidates,
		NextDNSAt: b.nextDNSAt.Unix(), LastFallbackReason: b.lastFallbackReason,
		CurrentNodeFailureCount: failCnt, CurrentNodeOpenUntil: openUntil, UpdatedAt: b.updatedAt.Unix(),
	})
	if err != nil {
		return `{"last_error":"status marshal failed"}`
	}
	return string(out)
}

func (b *SDKBootstrap) SetDeviceUUID(uuid string) error {
	b.mu.Lock(); defer b.mu.Unlock()
	if uuid == "" {
		return b.failLocked("empty device uuid")
	}
	b.deviceUUID = uuid
	b.updatedAt = time.Now()
	return nil
}
func (b *SDKBootstrap) SetCacheFile(path string) error {
	b.mu.Lock(); defer b.mu.Unlock()
	if strings.TrimSpace(path) == "" {
		return b.failLocked("empty cache file path")
	}
	b.cachePath = path
	b.updatedAt = time.Now()
	return nil
}

func (b *SDKBootstrap) tryDomainFallbackLocked() {
	now := time.Now()
	if b.payload == nil || b.payload.AppDomain == "" {
		b.state.EnterEmergency()
		return
	}
	if !b.nextDNSAt.IsZero() && now.Before(b.nextDNSAt) {
		return
	}
	port := b.client.config.ServerPort
	if port <= 0 {
		port = 443
	}
	host, err := resolveAppDomainHost(b.payload.AppDomain)
	if err != nil {
		b.state.EnterEmergency()
		b.nextDNSAt = now.Add(randomDNSInterval())
		return
	}
	b.client.config.ServerHost, b.client.config.ServerPort = host, port
	b.currentNode = net.JoinHostPort(host, strconv.Itoa(port))
	b.lastFallbackReason = "app_domain_dns"
	b.nextDNSAt = now.Add(randomDNSInterval())
}

func resolveAppDomainHost(domain string) (string, error) {
	d := strings.TrimSpace(domain)
	if strings.HasPrefix(d, "*.") {
		d = randomLabel(8) + d[1:]
	}
	ips, err := net.LookupHost(d)
	if err != nil {
		return "", err
	}
	if len(ips) == 0 {
		return "", errors.New("no ip resolved from app_domain")
	}
	return ips[0], nil
}
func randomDNSInterval() time.Duration { return time.Duration(20+(time.Now().UnixNano()%11)) * time.Second }
func randomLabel(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	if n <= 0 {
		return "x"
	}
	buf, rnd := make([]byte, n), make([]byte, n)
	if _, err := rand.Read(rnd); err != nil {
		return "x" + strconv.FormatInt(time.Now().UnixNano()%999999, 10)
	}
	for i := 0; i < n; i++ {
		buf[i] = letters[int(rnd[i])%len(letters)]
	}
	return string(buf)
}

func newDeviceUUID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
func parseEndpoint(endpoint string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(endpoint)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("bad port")
	}
	return host, port, nil
}
func (b *SDKBootstrap) fail(msg string) error { b.mu.Lock(); defer b.mu.Unlock(); return b.failLocked(msg) }
func (b *SDKBootstrap) failLocked(msg string) error {
	b.lastError = msg
	b.updatedAt = time.Now()
	return errors.New(msg)
}

