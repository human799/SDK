package sdk

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type NodeHealth struct {
    Node       string
    Healthy    bool
    LastCheck  time.Time
    RTT        time.Duration
    LastSuccess time.Time
}

type NodeHealthChecker struct {
    mu       sync.Mutex
    health   map[string]NodeHealth
    interval time.Duration
    timeout  time.Duration
    stop     chan struct{}
    running  bool
}

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
    dataDir     string
    uuidPath    string
    nextDNSAt   time.Time

    lastFallbackReason string
    lastNetworkOK      bool
    lastNetworkCheckAt time.Time
    prepared           bool
    lastError          string
    updatedAt          time.Time
    autoRefreshInterval time.Duration
    autoRefreshStop     chan struct{}
    autoRefreshRunning  bool

    // Extreme scenarios improvements
    stateMinDwellSec           int
    networkSwitchProtectSec    int
    networkSwitchLastAt        time.Time
    dnsRefreshShortSec         int
    dnsRefreshLongSec          int
    dnsPersistFailures         int
    dnsPersistFailuresCount    int
    highAvailabilityHeartbeatSec int
    recentSuccessPriority      bool
    lastSuccessNode            string

    // Node health checker
    healthChecker *NodeHealthChecker

	// explicitDataDir: SetDataDir was called; skip automatic persistence root.
	explicitDataDir bool
	// cachePathExplicit: SetCacheFile was called; keep cache path, derive uuid dir from it unless explicitDataDir is set.
	cachePathExplicit bool
}

func NewSDKBootstrap() *SDKBootstrap {
	b := &SDKBootstrap{
		client:    NewDefaultProxyClient(),
		state:     NewStateMachine(),
		breaker:   NewCircuitBreaker(),
		cachePath: "sdk_cache.json",
		dataDir:   ".",
		uuidPath:  "sdk_device_uuid.txt",
		autoRefreshInterval: 10 * time.Second,
		updatedAt: time.Now(),
		// Default values for extreme scenarios improvements
		stateMinDwellSec:           5,
		networkSwitchProtectSec:    8,
		networkSwitchLastAt:        time.Now(),
		dnsRefreshShortSec:         25,
		dnsRefreshLongSec:          90,
		dnsPersistFailures:         3,
		highAvailabilityHeartbeatSec: 60,
		recentSuccessPriority:      true,
		// Node health checker
		healthChecker: &NodeHealthChecker{
			health:   make(map[string]NodeHealth),
			interval: 5 * time.Minute,
			timeout:  3 * time.Second,
		},
	}
	b.client.SetConnectResultHook(func(success bool) {
		node := b.currentNodeSnapshot()
		if node == "" {
			return
		}
		b.ReportConnectResult(node, success)
	})
	return b
}

func (b *SDKBootstrap) ConfigureStateMachine(cfg StateMachineConfig) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.state = NewStateMachineWithConfig(cfg)
	if cfg.StateMinDwellSec > 0 {
		b.stateMinDwellSec = cfg.StateMinDwellSec
	}
	b.updatedAt = time.Now()
}
func (b *SDKBootstrap) ConfigureCircuitBreaker(cfg CircuitBreakerConfig) {
	b.mu.Lock()
	defer b.mu.Unlock()
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
	b.mu.Lock()
	b.dnsRefreshShortSec = p.DNSRefreshShortSec
	b.dnsRefreshLongSec = p.DNSRefreshLongSec
	b.dnsPersistFailures = p.DNSPersistFailures
	b.networkSwitchProtectSec = p.NetworkSwitchProtectSec
	b.highAvailabilityHeartbeatSec = p.HighAvailabilityHeartbeatSec
	b.recentSuccessPriority = p.RecentSuccessPriority
	// Update health checker config
	if p.HealthCheckIntervalSec > 0 {
		b.healthChecker.interval = time.Duration(p.HealthCheckIntervalSec) * time.Second
	}
	if p.HealthCheckTimeoutMs > 0 {
		b.healthChecker.timeout = time.Duration(p.HealthCheckTimeoutMs) * time.Millisecond
	}
	b.mu.Unlock()
	return nil
}
func (b *SDKBootstrap) LoadConfigFile(path string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := LoadEmbeddedPrivateKeyFromConfigFile(path); err != nil {
		return b.failLocked(fmt.Sprintf("load config file failed: %v", err))
	}
	b.lastError = ""
	b.updatedAt = time.Now()
	return nil
}
func (b *SDKBootstrap) Init(secret string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.applyAutoDataDirLocked(); err != nil {
		return b.failLocked(fmt.Sprintf("sdk data dir: %v", err))
	}
	if secret == "" {
		return b.failLocked("empty secret")
	}
	payload, err := DecodeSecretPayload(secret)
	if err != nil {
		return b.failLocked(fmt.Sprintf("decode secret failed: %v", err))
	}
	b.secretToken, b.payload = secret, payload
	if b.deviceUUID == "" {
		b.loadOrCreateDeviceUUIDLocked()
	}
	b.prepared, b.lastError, b.updatedAt = false, "", time.Now()
	// Reset health checker on init
	b.healthChecker.health = make(map[string]NodeHealth)
	// Reset last success node on init
	b.lastSuccessNode = ""
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
		// Startup fallback: if control-plane fetch fails, try app_domain directly
		// so the app can still boot in degraded mode.
		if payloadCopy.AppDomain != "" {
			host, derr := resolveAppDomainHost(payloadCopy.AppDomain)
			if derr == nil {
				port := 443
				b.mu.Lock()
				b.client.config.ServerHost = host
				b.client.config.ServerPort = port
				b.currentNode = net.JoinHostPort(host, strconv.Itoa(port))
				b.fixedSet = FixedNodeSet{}
				b.candidates = nil
				b.lastFallbackReason = "prepare_control_plane_failed_use_domain"
				b.prepared, b.lastError, b.updatedAt = true, "", time.Now()
				b.mu.Unlock()
				return nil
			}
		}
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
	// Fast-path: if selected primary is unreachable, immediately try app_domain
	// fallback so app startup is not blocked by a known bad node.
	if !isEndpointReachable(host, port, 1500*time.Millisecond) && payloadCopy.AppDomain != "" {
		if dh, derr := resolveAppDomainHost(payloadCopy.AppDomain); derr == nil {
			host = dh
			primary = net.JoinHostPort(host, strconv.Itoa(port))
		}
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
	b.mu.Lock()
	defer b.mu.Unlock()
	if host == "" || port <= 0 {
		return b.failLocked("invalid server endpoint")
	}
	b.client.config.ServerHost, b.client.config.ServerPort = host, port
	b.updatedAt = time.Now()
	return nil
}
func (b *SDKBootstrap) SetLocalPort(port int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
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
	b.startAutoRefreshLocked()
	b.startHealthCheckLoop()
	b.lastError, b.updatedAt = "", time.Now()
	b.mu.Unlock()
	return nil
}
func (b *SDKBootstrap) Stop() {
	b.mu.Lock()
	c := b.client
	b.stopAutoRefreshLocked()
	b.stopHealthCheckLoop()
	b.mu.Unlock()
	c.Stop()
	b.mu.Lock()
	b.updatedAt = time.Now()
	b.mu.Unlock()
}

// ResetHealthChecker resets the health checker state.
func (b *SDKBootstrap) ResetHealthChecker() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.healthChecker.health = make(map[string]NodeHealth)
}
func (b *SDKBootstrap) LocalPort() int {
	b.mu.Lock()
	c := b.client
	b.mu.Unlock()
	return c.LocalPort()
}
func (b *SDKBootstrap) IsRunning() bool {
	b.mu.Lock()
	c := b.client
	b.mu.Unlock()
	return c.IsRunning()
}

func (b *SDKBootstrap) ReportConnectResult(node string, success bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if success {
		b.breaker.RecordSuccess(node)
		// Record recent success node for fallback priority
		if b.recentSuccessPriority && node == b.currentNode {
			b.lastSuccessNode = node
		}
	} else {
		b.breaker.RecordFailure(node, time.Now())
		if node == b.currentNode {
			switched := false
			// Use recent success priority if enabled
			if b.recentSuccessPriority && b.lastSuccessNode != "" && b.lastSuccessNode != node {
				if b.breaker.Allow(b.lastSuccessNode, time.Now()) {
					host, port, err := parseEndpoint(b.lastSuccessNode)
					if err == nil {
						b.client.config.ServerHost, b.client.config.ServerPort = host, port
						b.currentNode = b.lastSuccessNode
						b.candidates = StableFallbackOrderWithRecentSuccess(b.deviceUUID, b.fixedSet, b.currentNode, b.lastSuccessNode)
						b.lastFallbackReason = "recent_success_fallback"
						switched = true
					}
				}
			}
			if !switched {
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
					switched = true
					break
				}
			}
			if !switched {
				// Distinguish local network failure from node-specific failure.
				b.lastNetworkOK = checkBaiduReachable()
				b.lastNetworkCheckAt = time.Now()
				if !b.lastNetworkOK {
					b.lastFallbackReason = "network_unreachable"
				}
				b.tryDomainFallbackLocked()
			}
		}
	}
	// Check if state can transition based on minimum dwell time
	if b.state.CanTransition() {
		b.state.RecordResult(success)
	}
	
	// Lazy health check on failure
	if !success && node == b.currentNode {
		go b.lazyCheckCandidates()
	}
	
	b.updatedAt = time.Now()
}
func (b *SDKBootstrap) CanTryNode(node string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.breaker.Allow(node, time.Now())
}
func (b *SDKBootstrap) Status() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	type diagnostic struct {
		StateMinDwellRemainingSec int    `json:"state_min_dwell_remaining_sec,omitempty"`
		NetworkSwitchProtectSec   int    `json:"network_switch_protect_remaining_sec,omitempty"`
		DNSRefreshIntervalSec     int    `json:"dns_refresh_interval_sec,omitempty"`
		DNSPersistFailuresCount   int    `json:"dns_persist_failures_count,omitempty"`
		RecentSuccessFallbackCount int   `json:"recent_success_fallback_count,omitempty"`
		LastSuccessNode           string `json:"last_success_node,omitempty"`
	}
	type status struct {
		Prepared                bool           `json:"prepared"`
		Running                 bool           `json:"running"`
		LocalPort               int            `json:"local_port"`
		ServerHost              string         `json:"server_host"`
		ServerPort              int            `json:"server_port"`
		LastError               string         `json:"last_error,omitempty"`
		Payload                 *SecretPayload `json:"payload,omitempty"`
		State                   SDKState       `json:"state"`
		DeviceUUID              string         `json:"device_uuid,omitempty"`
		FixedSet                FixedNodeSet   `json:"fixed_set"`
		Current                 string         `json:"current_node,omitempty"`
		Candidates              []string       `json:"candidates,omitempty"`
		NextDNSAt               int64          `json:"next_dns_refresh_unix,omitempty"`
		LastFallbackReason      string         `json:"last_fallback_reason,omitempty"`
		CurrentNodeFailureCount int            `json:"current_node_failures"`
		CurrentNodeOpenUntil    int64          `json:"current_node_open_until_unix,omitempty"`
		LastNetworkOK           bool           `json:"last_network_ok"`
		LastNetworkCheckAt      int64          `json:"last_network_check_unix,omitempty"`
		UpdatedAt               int64          `json:"updated_at_unix"`
		Diagnostic              *diagnostic    `json:"diagnostic,omitempty"`
	}
	failCnt, openUntil := 0, int64(0)
	if b.currentNode != "" {
		failCnt = b.breaker.FailureCount(b.currentNode)
		openUntil = b.breaker.OpenUntil(b.currentNode).Unix()
	}
	// Calculate diagnostic info
	diag := &diagnostic{}
	if b.stateMinDwellSec > 0 {
		elapsed := time.Since(b.state.lastStateEnteredAt).Seconds()
		if elapsed < float64(b.stateMinDwellSec) {
			diag.StateMinDwellRemainingSec = int(float64(b.stateMinDwellSec) - elapsed)
		}
	}
	if b.networkSwitchProtectSec > 0 {
		elapsed := time.Since(b.networkSwitchLastAt).Seconds()
		if elapsed < float64(b.networkSwitchProtectSec) {
			diag.NetworkSwitchProtectSec = int(float64(b.networkSwitchProtectSec) - elapsed)
		}
	}
	if b.dnsPersistFailuresCount <= b.dnsPersistFailures {
		diag.DNSRefreshIntervalSec = b.dnsRefreshShortSec
	} else {
		diag.DNSRefreshIntervalSec = b.dnsRefreshLongSec
	}
	diag.DNSPersistFailuresCount = b.dnsPersistFailuresCount
	diag.RecentSuccessFallbackCount = 0 // TODO: track this
	diag.LastSuccessNode = b.lastSuccessNode
	out, err := json.Marshal(status{
		Prepared: b.prepared, Running: b.client.IsRunning(), LocalPort: b.client.LocalPort(),
		ServerHost: b.client.config.ServerHost, ServerPort: b.client.config.ServerPort,
		LastError: b.lastError, Payload: b.payload, State: b.state.State(), DeviceUUID: b.deviceUUID,
		FixedSet: b.fixedSet, Current: b.currentNode, Candidates: b.candidates,
		NextDNSAt: b.nextDNSAt.Unix(), LastFallbackReason: b.lastFallbackReason,
		CurrentNodeFailureCount: failCnt, CurrentNodeOpenUntil: openUntil, UpdatedAt: b.updatedAt.Unix(),
		LastNetworkOK: b.lastNetworkOK, LastNetworkCheckAt: b.lastNetworkCheckAt.Unix(),
		Diagnostic: diag,
	})
	if err != nil {
		return `{"last_error":"status marshal failed"}`
	}
	return string(out)
}

func (b *SDKBootstrap) SetDeviceUUID(uuid string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.applyAutoDataDirLocked(); err != nil {
		return b.failLocked(fmt.Sprintf("sdk data dir: %v", err))
	}
	if uuid == "" {
		return b.failLocked("empty device uuid")
	}
	b.deviceUUID = uuid
	_ = b.persistDeviceUUIDLocked()
	b.updatedAt = time.Now()
	return nil
}

// SetAutoRefreshIntervalSec sets background refresh interval seconds.
// sec <= 0 disables auto-refresh.
func (b *SDKBootstrap) SetAutoRefreshIntervalSec(sec int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if sec <= 0 {
		b.autoRefreshInterval = 0
		b.stopAutoRefreshLocked()
		b.updatedAt = time.Now()
		return
	}
	b.autoRefreshInterval = time.Duration(sec) * time.Second
	if b.autoRefreshRunning {
		b.stopAutoRefreshLocked()
		b.startAutoRefreshLocked()
	}
	b.updatedAt = time.Now()
}

// applyAutoDataDirLocked chooses a persistent directory when the app did not call SetDataDir.
func (b *SDKBootstrap) applyAutoDataDirLocked() error {
	if b.explicitDataDir {
		return nil
	}
	if b.cachePathExplicit {
		dir := filepath.Dir(b.cachePath)
		if dir == "" {
			dir = "."
		}
		b.dataDir = dir
		b.uuidPath = filepath.Join(dir, "sdk_device_uuid.txt")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		return nil
	}
	for _, dir := range autoSDKDataDirCandidates() {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			continue
		}
		b.dataDir = dir
		b.cachePath = filepath.Join(dir, "sdk_cache.json")
		b.uuidPath = filepath.Join(dir, "sdk_device_uuid.txt")
		return nil
	}
	// Same layout as pre–auto-datadir SDK: cwd files (always mkdir-able).
	b.dataDir = "."
	b.cachePath = "sdk_cache.json"
	b.uuidPath = "sdk_device_uuid.txt"
	return nil
}

// SetDataDir sets cross-platform SDK data directory for cache/uuid files.
// Optional: if never called, Init applies a default (app config dir / Android sandbox).
func (b *SDKBootstrap) SetDataDir(path string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	p := strings.TrimSpace(path)
	if p == "" {
		return b.failLocked("empty data dir")
	}
	b.explicitDataDir = true
	b.cachePathExplicit = false
	b.dataDir = p
	b.cachePath = filepath.Join(p, "sdk_cache.json")
	b.uuidPath = filepath.Join(p, "sdk_device_uuid.txt")
	b.updatedAt = time.Now()
	return nil
}

// DeviceUUID returns current in-memory device uuid.
func (b *SDKBootstrap) DeviceUUID() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.deviceUUID
}
func (b *SDKBootstrap) SetCacheFile(path string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if strings.TrimSpace(path) == "" {
		return b.failLocked("empty cache file path")
	}
	b.cachePathExplicit = true
	b.cachePath = path
	b.updatedAt = time.Now()
	return nil
}

// SetNetworkSwitch marks that a network switch has occurred.
// This triggers a protection window during which熔断升级 is suspended.
func (b *SDKBootstrap) SetNetworkSwitch() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.networkSwitchLastAt = time.Now()
	b.lastNetworkCheckAt = time.Now()
	b.lastNetworkOK = true
	// Trigger immediate control plane refresh after network switch
	go b.refreshControlPlaneOnce()
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
		b.dnsPersistFailuresCount++
		// Dual-layer DNS refresh: short period first, then long period for persistent failures
		var nextInterval time.Duration
		if b.dnsPersistFailuresCount <= b.dnsPersistFailures {
			nextInterval = time.Duration(b.dnsRefreshShortSec) * time.Second
		} else {
			nextInterval = time.Duration(b.dnsRefreshLongSec) * time.Second
		}
		b.state.EnterEmergency()
		b.nextDNSAt = now.Add(nextInterval)
		return
	}
	// Reset failure count on success
	b.dnsPersistFailuresCount = 0
	b.client.config.ServerHost, b.client.config.ServerPort = host, port
	b.currentNode = net.JoinHostPort(host, strconv.Itoa(port))
	b.lastFallbackReason = "app_domain_dns"
	b.nextDNSAt = now.Add(time.Duration(b.dnsRefreshShortSec) * time.Second)
	// Immediately try to refresh control plane after domain fallback
	// This allows faster recovery when control plane is back online
	go b.refreshControlPlaneOnce()
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
func randomDNSInterval() time.Duration {
	return time.Duration(20+(time.Now().UnixNano()%11)) * time.Second
}
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

func isEndpointReachable(host string, port int, timeout time.Duration) bool {
	if host == "" || port <= 0 {
		return false
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func checkBaiduReachable() bool {
	// Android environments may block ICMP ping; TCP probe is more reliable.
	conn, err := net.DialTimeout("tcp", "www.baidu.com:443", 2*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
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
func (b *SDKBootstrap) fail(msg string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.failLocked(msg)
}
func (b *SDKBootstrap) failLocked(msg string) error {
	b.lastError = msg
	b.updatedAt = time.Now()
	return errors.New(msg)
}

func (b *SDKBootstrap) loadOrCreateDeviceUUIDLocked() {
	if b.deviceUUID != "" {
		return
	}
	if s, err := os.ReadFile(b.uuidPath); err == nil {
		v := strings.TrimSpace(string(s))
		if v != "" {
			b.deviceUUID = v
			return
		}
	}
	b.deviceUUID = newDeviceUUID()
	_ = b.persistDeviceUUIDLocked()
}

func (b *SDKBootstrap) persistDeviceUUIDLocked() error {
	if b.deviceUUID == "" {
		return nil
	}
	dir := filepath.Dir(b.uuidPath)
	if dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	return os.WriteFile(b.uuidPath, []byte(b.deviceUUID), 0o600)
}

func (b *SDKBootstrap) currentNodeSnapshot() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.currentNode
}

func (b *SDKBootstrap) startAutoRefreshLocked() {
	if b.autoRefreshRunning || b.autoRefreshInterval <= 0 {
		return
	}
	stop := make(chan struct{})
	b.autoRefreshStop = stop
	b.autoRefreshRunning = true
	interval := b.autoRefreshInterval
	go b.autoRefreshLoop(stop, interval)
}

func (b *SDKBootstrap) stopAutoRefreshLocked() {
	if !b.autoRefreshRunning {
		return
	}
	close(b.autoRefreshStop)
	b.autoRefreshStop = nil
	b.autoRefreshRunning = false
}

func (b *SDKBootstrap) autoRefreshLoop(stop <-chan struct{}, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			b.refreshControlPlaneOnce()
		}
	}
}

func (b *SDKBootstrap) refreshControlPlaneOnce() {
	b.mu.Lock()
	if b.payload == nil || !b.prepared {
		b.mu.Unlock()
		return
	}
	payloadCopy := *b.payload
	deviceUUID := b.deviceUUID
	cachePath := b.cachePath
	highAvailabilityHeartbeatSec := b.highAvailabilityHeartbeatSec
	b.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cacheData, _ := loadSDKCache(cachePath)
	groups, updatedCache, err := resolveControlPlane(ctx, &payloadCopy, cacheData)
	if err != nil {
		return
	}
	_ = saveSDKCache(cachePath, updatedCache)
	set := SelectFixedNodeSet(deviceUUID, groups)
	nodes := set.AsList()
	if len(nodes) == 0 {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	cur := b.currentNode
	inSet := false
	for _, n := range nodes {
		if n == cur {
			inSet = true
			break
		}
	}
	if !inSet {
		cur = nodes[0]
		if host, port, perr := parseEndpoint(cur); perr == nil {
			b.client.config.ServerHost = host
			b.client.config.ServerPort = port
			b.currentNode = cur
		}
	}
	b.fixedSet = set
	b.candidates = StableFallbackOrder(deviceUUID, set, b.currentNode)
	b.updatedAt = time.Now()

	// High availability heartbeat for nodesE (if configured)
	if highAvailabilityHeartbeatSec > 0 && len(groups.E) > 0 {
		go b.checkHighAvailabilityNodes(groups.E, highAvailabilityHeartbeatSec)
	}

	// Active switch: check if there's a better node
	b.checkAndSwitchToBetterNode()
}

// checkHighAvailabilityNodes periodically probes high availability nodes (nodesE).
func (b *SDKBootstrap) checkHighAvailabilityNodes(nodes []string, intervalSec int) {
	if len(nodes) == 0 {
		return
	}
	// Probe the first HA node
	host, port, err := parseEndpoint(nodes[0])
	if err != nil {
		return
	}
	// Use a non-blocking check with timeout
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 2*time.Second)
	if err == nil {
		conn.Close()
	}
}

// startHealthCheckLoop starts the background health check goroutine.
func (b *SDKBootstrap) startHealthCheckLoop() {
	if b.healthChecker.running {
		return
	}
	stop := make(chan struct{})
	b.healthChecker.stop = stop
	b.healthChecker.running = true
	go b.healthCheckLoop(stop, b.healthChecker.interval)
}

// stopHealthCheckLoop stops the background health check goroutine.
func (b *SDKBootstrap) stopHealthCheckLoop() {
	if !b.healthChecker.running {
		return
	}
	close(b.healthChecker.stop)
	b.healthChecker.stop = nil
	b.healthChecker.running = false
}

// healthCheckLoop runs periodic health checks.
func (b *SDKBootstrap) healthCheckLoop(stop <-chan struct{}, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			b.checkAllNodesHealth()
		}
	}
}

// checkAllNodesHealth checks health of all nodes concurrently.
func (b *SDKBootstrap) checkAllNodesHealth() {
	b.mu.Lock()
	if !b.prepared {
		b.mu.Unlock()
		return
	}
	// Get all nodes from fixed set and candidates
	allNodes := b.getAllNodesLocked()
	timeout := b.healthChecker.timeout
	b.mu.Unlock()

	if len(allNodes) == 0 {
		return
	}

	// Concurrently check all nodes
	healthy := b.concurrentCheck(allNodes, timeout)

	// Update health status
	b.mu.Lock()
	for _, h := range healthy {
		b.healthChecker.health[h.Node] = h
	}
	b.mu.Unlock()
}

// getAllNodesLocked returns all nodes from fixed set and candidates.
func (b *SDKBootstrap) getAllNodesLocked() []string {
	nodes := make(map[string]bool)
	
	// Add nodes from fixed set
	for _, n := range b.fixedSet.AsList() {
		nodes[n] = true
	}
	
	// Add candidates
	for _, n := range b.candidates {
		nodes[n] = true
	}
	
	// Convert to slice
	result := make([]string, 0, len(nodes))
	for n := range nodes {
		result = append(result, n)
	}
	return result
}

// concurrentCheck checks multiple nodes concurrently.
func (b *SDKBootstrap) concurrentCheck(nodes []string, timeout time.Duration) []NodeHealth {
	if len(nodes) == 0 {
		return nil
	}
	
	results := make(chan NodeHealth, len(nodes))
	
	for _, node := range nodes {
		go func(n string) {
			health := b.checkSingleNode(n, timeout)
			results <- health
		}(node)
	}
	
	var healthy []NodeHealth
	for i := 0; i < len(nodes); i++ {
		select {
		case health := <-results:
			healthy = append(healthy, health)
		case <-time.After(timeout):
			return healthy
		}
	}
	return healthy
}

// checkSingleNode checks a single node's health.
func (b *SDKBootstrap) checkSingleNode(node string, timeout time.Duration) NodeHealth {
	host, port, err := parseEndpoint(node)
	if err != nil {
		return NodeHealth{Node: node, Healthy: false, LastCheck: time.Now()}
	}
	
	start := time.Now()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), timeout)
	rtt := time.Since(start)
	
	if err != nil {
		return NodeHealth{Node: node, Healthy: false, LastCheck: time.Now(), RTT: rtt}
	}
	conn.Close()
	
	return NodeHealth{Node: node, Healthy: true, LastCheck: time.Now(), RTT: rtt}
}

// getBestNode returns the best node based on health status.
func (b *SDKBootstrap) getBestNode() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	if len(b.healthChecker.health) == 0 {
		return ""
	}
	
	var bestNode string
	var bestScore float64 = -1
	
	for node, health := range b.healthChecker.health {
		if !health.Healthy {
			continue
		}
		
		// Score: healthy + RTT bonus
		score := 1.0 - (float64(health.RTT) / 10000.0) // RTT up to 10 seconds
		if score > bestScore {
			bestScore = score
			bestNode = node
		}
	}
	
	return bestNode
}

// updateNodeHealth updates the health status of a node.
func (b *SDKBootstrap) updateNodeHealth(health NodeHealth) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.healthChecker.health[health.Node] = health
}

// getHealth returns the health status of a node.
func (b *SDKBootstrap) getHealth(node string) NodeHealth {
	b.mu.Lock()
	defer b.mu.Unlock()
	if h, ok := b.healthChecker.health[node]; ok {
		return h
	}
	return NodeHealth{Node: node, Healthy: false}
}

// checkAndSwitchToBetterNode checks if there's a better node and switches if needed.
func (b *SDKBootstrap) checkAndSwitchToBetterNode() {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	if !b.prepared || len(b.candidates) == 0 {
		return
	}
	
	// Get best node from health check
	var bestNode string
	var bestScore float64 = -1
	
	for _, node := range b.candidates {
		if h, ok := b.healthChecker.health[node]; ok && h.Healthy {
			score := 1.0 - (float64(h.RTT) / 10000.0)
			if score > bestScore {
				bestScore = score
				bestNode = node
			}
		}
	}
	
	// If best node is different from current and not in breaker, switch
	if bestNode != "" && bestNode != b.currentNode && b.breaker.Allow(bestNode, time.Now()) {
		host, port, err := parseEndpoint(bestNode)
		if err == nil {
			b.client.config.ServerHost = host
			b.client.config.ServerPort = port
			b.currentNode = bestNode
			b.candidates = StableFallbackOrder(b.deviceUUID, b.fixedSet, b.currentNode)
			b.lastFallbackReason = "health_check_switch"
		}
	}
}

// lazyCheckCandidates performs lazy health check on candidates after failure.
func (b *SDKBootstrap) lazyCheckCandidates() {
	b.mu.Lock()
	if len(b.candidates) == 0 {
		b.mu.Unlock()
		return
	}
	candidates := b.candidates
	timeout := b.healthChecker.timeout
	b.mu.Unlock()

	// Check candidates concurrently
	healthy := b.concurrentCheck(candidates, timeout)

	// Update health status
	b.mu.Lock()
	for _, h := range healthy {
		b.healthChecker.health[h.Node] = h
	}
	b.mu.Unlock()
}
