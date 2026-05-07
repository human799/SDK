// Package sdk provides a client proxy SDK compatible with Android via gomobile.
package sdk

import (
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"sync"
	"time"

	utls "github.com/refraction-networking/utls"
)

// sniPool is the list of SNI hostnames randomly selected per connection to
// blend traffic with common Chinese CDN/cloud domains.
var sniPool = []string{
	"share.note.youdao.com",
	"mail.163.com",
	"www.sohu.com",
	"im.qq.com",
	"www.baidu.com",
	"www.zhihu.com",
	"static.zhihu.com",
	"res.wx.qq.com",
	"open.weixin.qq.com",
	"music.163.com",
}

// randomSNI returns a random SNI hostname from the pool.
func randomSNI() string {
	return sniPool[rand.Intn(len(sniPool))]
}

// helloIDs is the pool of uTLS ClientHello presets to rotate through,
// impersonating real browsers/OS TLS stacks.
var helloIDs = []utls.ClientHelloID{
	utls.HelloChrome_133,
	utls.HelloChrome_120,
	utls.HelloChrome_106_Shuffle,
	utls.HelloFirefox_120,
	utls.HelloFirefox_105,
	utls.HelloIOS_14,
	utls.HelloAndroid_11_OkHttp,
}

// randomHelloID returns a random uTLS ClientHello preset.
func randomHelloID() utls.ClientHelloID {
	return helloIDs[rand.Intn(len(helloIDs))]
}

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

	log.Printf("[SDK] started, local %s -> server %s:%d (TLS=%v)",
		ln.Addr(), c.config.ServerHost, c.config.ServerPort, c.config.TLSEnabled)

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

	if c.config.TLSEnabled {
		sni := randomSNI()
		helloID := randomHelloID()

		dialer := &net.Dialer{Timeout: timeout}
		rawConn, err := dialer.Dial("tcp", serverAddr)
		if err != nil {
			log.Printf("[SDK] TCP connect failed %s: %v", serverAddr, err)
			return
		}

		// uTLS: impersonate a real browser/OS TLS fingerprint
		tlsConn := utls.UClient(rawConn, &utls.Config{
			ServerName:         sni,
			InsecureSkipVerify: true, // self-signed cert on server
		}, helloID)

		tlsConn.SetDeadline(time.Now().Add(timeout))
		if err := tlsConn.Handshake(); err != nil {
			rawConn.Close()
			log.Printf("[SDK] uTLS handshake failed (SNI=%s hello=%s): %v", sni, helloID.Client, err)
			return
		}
		tlsConn.SetDeadline(time.Time{}) // clear deadline after handshake
		defer tlsConn.Close()
		log.Printf("[SDK] uTLS connected SNI=%s hello=%s -> %s", sni, helloID.Client, serverAddr)

		obfsRemote := newObfsConn(tlsConn)
		relayObfs(local, obfsRemote)
	} else {
		remote, err := net.DialTimeout("tcp", serverAddr, timeout)
		if err != nil {
			log.Printf("[SDK] connect to server failed %s: %v", serverAddr, err)
			return
		}
		defer remote.Close()
		relay(local, remote)
	}
}

// relayObfs pipes between a plain local conn and an obfs-wrapped remote conn.
// local→remote: read raw bytes, encode into obfs frames
// remote→local: decode obfs frames, write raw payload to local
func relayObfs(local net.Conn, remote *obfsConn) {
	var wg sync.WaitGroup
	wg.Add(2)

	// local → remote: encode into obfs frames
	go func() {
		defer wg.Done()
		buf := make([]byte, obfsMaxFrame)
		for {
			n, err := local.Read(buf)
			if n > 0 {
				if _, werr := remote.Write(buf[:n]); werr != nil {
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
				return
			}
		}
	}()

	wg.Wait()
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
