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
}

// NewProxyConfig creates a new ProxyConfig.
func NewProxyConfig(serverHost string, serverPort int) *ProxyConfig {
	return &ProxyConfig{
		ServerHost:  serverHost,
		ServerPort:  serverPort,
		LocalHost:   "127.0.0.1",
		LocalPort:   0,
		DialTimeout: 10,
	}
}

// NewDefaultProxyClient creates a client with hardcoded demo config.
// Forwarding server: 216.118.241.194:10443
// Local listen:      127.23.6.1:9527
func NewDefaultProxyClient() *ProxyClient {
	return &ProxyClient{
		config: &ProxyConfig{
			ServerHost:  "216.118.241.194",
			ServerPort:  10443,
			LocalHost:   "127.23.6.1",
			LocalPort:   9527,
			DialTimeout: 10,
		},
		done: make(chan struct{}),
	}
}

// ProxyClient is a local TCP proxy that forwards traffic to the remote server.
type ProxyClient struct {
	config   *ProxyConfig
	listener net.Listener
	mu       sync.Mutex
	running  bool
	done     chan struct{}
}

// NewProxyClient creates a new ProxyClient instance.
func NewProxyClient(config *ProxyConfig) *ProxyClient {
	return &ProxyClient{
		config: config,
		done:   make(chan struct{}),
	}
}

// Start starts the local listener.
func (c *ProxyClient) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.running {
		return fmt.Errorf("proxy is already running")
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

	log.Printf("[SDK] started, local %s -> server %s:%d",
		ln.Addr(), c.config.ServerHost, c.config.ServerPort)

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

	serverAddr := net.JoinHostPort(c.config.ServerHost, fmt.Sprintf("%d", c.config.ServerPort))
	timeout := time.Duration(c.config.DialTimeout) * time.Second

	remote, err := net.DialTimeout("tcp", serverAddr, timeout)
	if err != nil {
		log.Printf("[SDK] connect to server failed %s: %v", serverAddr, err)
		return
	}
	defer remote.Close()

	relay(local, remote)
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
