// Package sdk provides a client proxy SDK compatible with Android via gomobile.
package sdk

import (
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

// ProxyConfig holds the configuration for the proxy client.
type ProxyConfig struct {
	// ServerHost is the forwarding server address.
	ServerHost string
	// ServerPort is the forwarding server port.
	ServerPort int
	// LocalHost is the local listen address, default 127.0.0.1.
	LocalHost string
	// LocalPort is the local listen port, 0 means random.
	LocalPort int
	// DialTimeout is the connection timeout in seconds.
	DialTimeout int
	// TLSEnabled controls whether TLS + obfs is used (default true).
	TLSEnabled bool
}

// NewProxyConfig creates a new ProxyConfig.
func NewProxyConfig(serverHost string, serverPort int) *ProxyConfig {
	return &ProxyConfig{
		ServerHost:  serverHost,
		ServerPort:  serverPort,
		LocalHost:   "127.0.0.1",
		LocalPort:   0,
		DialTimeout: 10,
		TLSEnabled:  true,
	}
}

// NewDefaultProxyClient creates a client with hardcoded demo config.
// Forwarding server: 216.*.*.194:10443
// Local listen:      127.23.6.1:9527
func NewDefaultProxyClient() *ProxyClient {
	return &ProxyClient{
		config: &ProxyConfig{
			ServerHost:  "",
			ServerPort:  0,
			LocalHost:   "127.0.0.1",
			LocalPort:   0,
			DialTimeout: 10,
			TLSEnabled:  true,
		},
		done: make(chan struct{}),
	}
}

// ProxyClient is a local TCP proxy that forwards traffic to the remote server
// over TLS with SNI rotation and obfuscation framing.
type ProxyClient struct {
	config   *ProxyConfig
	listener net.Listener
	pool     *connPool
	mu       sync.Mutex
	running  bool
	done     chan struct{}
	onConnectResult func(success bool)
}

// NewProxyClient creates a new ProxyClient instance.
func NewProxyClient(config *ProxyConfig) *ProxyClient {
	return &ProxyClient{
		config: config,
		done:   make(chan struct{}),
	}
}

// SetConnectResultHook sets a callback for per-connection result reporting.
// success=false means connect/relay failed; success=true means one relay session completed.
func (c *ProxyClient) SetConnectResultHook(hook func(success bool)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onConnectResult = hook
}

// Start starts the local listener.
func (c *ProxyClient) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.running {
		return fmt.Errorf("proxy is already running")
	}
	if c.config.ServerHost == "" || c.config.ServerPort <= 0 {
		return fmt.Errorf("server config is required: empty host or invalid port")
	}

	localHost := c.config.LocalHost
	if localHost == "" {
		localHost = "127.0.0.1"
	}

	addr := fmt.Sprintf("%s:%d", localHost, c.config.LocalPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen failed: %w", err)
	}

	c.listener = ln
	c.running = true
	c.done = make(chan struct{})

	if c.config.TLSEnabled {
		c.pool = newConnPool(c.config)
	}

	log.Printf("[SDK] started, local %s -> server %s:%d (TLS=%v)",
		ln.Addr(), maskIP(c.config.ServerHost), c.config.ServerPort, c.config.TLSEnabled)

	go c.acceptLoop()
	return nil
}

// Stop stops the proxy and releases the port.
func (c *ProxyClient) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running {
		return
	}
	c.running = false
	close(c.done)
	c.listener.Close()
	if c.pool != nil {
		c.pool.close()
		c.pool = nil
	}
}

// LocalPort returns the actual local listening port after Start.
func (c *ProxyClient) LocalPort() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.listener == nil {
		return 0
	}
	return c.listener.Addr().(*net.TCPAddr).Port
}

// IsRunning returns whether the proxy is currently running.
func (c *ProxyClient) IsRunning() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}

func (c *ProxyClient) acceptLoop() {
	for {
		conn, err := c.listener.Accept()
		if err != nil {
			select {
			case <-c.done:
				return
			default:
				log.Printf("[SDK] accept error: %v", err)
				continue
			}
		}
		go c.handleConn(conn)
	}
}

func (c *ProxyClient) handleConn(local net.Conn) {
	defer local.Close()

	if c.config.TLSEnabled {
		pc := c.pool.get()
		if pc == nil {
			log.Printf("[SDK] pool: no connection available")
			c.reportConnectResult(false)
			return
		}
		err := relayObfs(local, pc.conn)
		if err != nil {
			// connection is broken, discard it
			c.pool.discard(pc)
			c.reportConnectResult(false)
		} else {
			c.pool.put(pc)
			c.reportConnectResult(true)
		}
		return
	}

	// plain TCP fallback
	serverAddr := net.JoinHostPort(c.config.ServerHost, fmt.Sprintf("%d", c.config.ServerPort))
	timeout := time.Duration(c.config.DialTimeout) * time.Second
	remote, err := net.DialTimeout("tcp", serverAddr, timeout)
	if err != nil {
		log.Printf("[SDK] connect to server failed %s: %v", maskIP(serverAddr), err)
		c.reportConnectResult(false)
		return
	}
	defer remote.Close()
	relay(local, remote)
	c.reportConnectResult(true)
}

// relayObfs pipes between a plain local conn and an obfs-wrapped remote conn.
// Returns a non-nil error if the remote connection encountered an error.
func relayObfs(local net.Conn, remote *obfsConn) error {
	var wg sync.WaitGroup
	wg.Add(2)
	var remoteErr error
	var mu sync.Mutex

	// local → remote: encode into obfs frames
	go func() {
		defer wg.Done()
		buf := make([]byte, obfsMaxFrame)
		for {
			n, err := local.Read(buf)
			if n > 0 {
				if _, werr := remote.Write(buf[:n]); werr != nil {
					mu.Lock()
					remoteErr = werr
					mu.Unlock()
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// remote → local: decode obfs frames, forward raw payload
	go func() {
		defer wg.Done()
		buf := make([]byte, obfsMaxFrame)
		for {
			n, err := remote.Read(buf)
			if n > 0 {
				if _, werr := local.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				mu.Lock()
				remoteErr = err
				mu.Unlock()
				return
			}
		}
	}()

	wg.Wait()
	return remoteErr
}

func relay(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	pipe := func(dst, src net.Conn) {
		defer wg.Done()
		if tc, ok := dst.(*net.TCPConn); ok {
			defer tc.CloseWrite()
		}
		io.Copy(dst, src)
	}

	go pipe(a, b)
	go pipe(b, a)
	wg.Wait()
}

func (c *ProxyClient) reportConnectResult(success bool) {
	c.mu.Lock()
	hook := c.onConnectResult
	c.mu.Unlock()
	if hook != nil {
		hook(success)
	}
}
