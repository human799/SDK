package sdk

import (
	"fmt"
	"log"
	"math/rand"
	"net"
	"strings"
	"sync"
	"time"

	utls "github.com/refraction-networking/utls"
)

// sniPool is the list of SNI hostnames randomly selected per connection.
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

func randomSNI() string { return sniPool[rand.Intn(len(sniPool))] }

// maskIP replaces the middle segments of an IP with *.
// "216.118.241.194" → "216.***.***.194"
// "2001:db8::1"     → "2001:***::1"
func maskIP(ip string) string {
	parts := strings.Split(ip, ".")
	if len(parts) == 4 {
		parts[1] = "***"
		parts[2] = "***"
		return strings.Join(parts, ".")
	}
	// IPv6: mask the middle
	parts = strings.Split(ip, ":")
	if len(parts) >= 3 {
		parts[1] = "***"
		return strings.Join(parts, ":")
	}
	return ip
}

// helloIDs is the pool of uTLS ClientHello presets, impersonating real clients.
var helloIDs = []utls.ClientHelloID{
	utls.HelloChrome_133,
	utls.HelloChrome_120,
	utls.HelloChrome_106_Shuffle,
	utls.HelloFirefox_120,
	utls.HelloFirefox_105,
	utls.HelloIOS_14,
	utls.HelloAndroid_11_OkHttp,
}

func randomHelloID() utls.ClientHelloID { return helloIDs[rand.Intn(len(helloIDs))] }

const (
	poolMinIdle   = 2               // minimum idle connections to keep warm
	poolMaxSize   = 8               // maximum total connections in pool
	poolMaxAge    = 10 * time.Minute // max lifetime of a connection
	poolIdleLimit = 3 * time.Minute  // close idle connections after this
)

// poolConn is a pooled obfs+TLS connection with metadata.
type poolConn struct {
	conn      *obfsConn
	createdAt time.Time
	lastUsed  time.Time
}

func (p *poolConn) expired() bool {
	now := time.Now()
	return now.Sub(p.createdAt) > poolMaxAge || now.Sub(p.lastUsed) > poolIdleLimit
}

// connPool manages a pool of reusable obfs+uTLS connections to the server.
type connPool struct {
	cfg   *ProxyConfig
	mu    sync.Mutex
	idle  []*poolConn
	total int
	done  chan struct{}
}

func newConnPool(cfg *ProxyConfig) *connPool {
	p := &connPool{
		cfg:  cfg,
		done: make(chan struct{}),
	}
	// pre-warm minimum idle connections
	for i := 0; i < poolMinIdle; i++ {
		if c := p.dial(); c != nil {
			p.idle = append(p.idle, c)
			p.total++
		}
	}
	go p.maintainLoop()
	return p
}

// get returns an idle connection or dials a new one.
func (p *connPool) get() *poolConn {
	p.mu.Lock()
	// find a non-expired idle connection
	for len(p.idle) > 0 {
		c := p.idle[len(p.idle)-1]
		p.idle = p.idle[:len(p.idle)-1]
		if !c.expired() {
			p.mu.Unlock()
			c.lastUsed = time.Now()
			return c
		}
		// expired: close and discard
		c.conn.Close()
		p.total--
	}
	p.mu.Unlock()

	// no idle connection available, dial a new one
	return p.dial()
}

// put returns a connection to the pool, or closes it if the pool is full.
func (p *connPool) put(c *poolConn) {
	if c == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if c.expired() || p.total > poolMaxSize {
		c.conn.Close()
		p.total--
		return
	}
	c.lastUsed = time.Now()
	p.idle = append(p.idle, c)
}

// discard closes a connection without returning it to the pool.
func (p *connPool) discard(c *poolConn) {
	if c == nil {
		return
	}
	c.conn.Close()
	p.mu.Lock()
	p.total--
	p.mu.Unlock()
}

// dial creates a new obfs+uTLS connection to the server.
func (p *connPool) dial() *poolConn {
	cfg := p.cfg
	serverAddr := net.JoinHostPort(cfg.ServerHost, itoa(cfg.ServerPort))
	timeout := time.Duration(cfg.DialTimeout) * time.Second

	sni := randomSNI()
	helloID := randomHelloID()

	rawConn, err := (&net.Dialer{Timeout: timeout}).Dial("tcp", serverAddr)
	if err != nil {
		log.Printf("[Pool] TCP dial failed %s: %v", maskIP(serverAddr), err)
		return nil
	}

	tlsConn := utls.UClient(rawConn, &utls.Config{
		ServerName:         sni,
		InsecureSkipVerify: true,
	}, helloID)

	tlsConn.SetDeadline(time.Now().Add(timeout))
	if err := tlsConn.Handshake(); err != nil {
		rawConn.Close()
		log.Printf("[Pool] uTLS handshake failed (SNI=%s hello=%s): %v", sni, helloID.Client, err)
		return nil
	}
	tlsConn.SetDeadline(time.Time{})

	log.Printf("[Pool] new conn SNI=%s hello=%s -> %s", sni, helloID.Client, maskIP(serverAddr))

	p.mu.Lock()
	p.total++
	p.mu.Unlock()

	return &poolConn{
		conn:      newObfsConn(tlsConn),
		createdAt: time.Now(),
		lastUsed:  time.Now(),
	}
}

// maintainLoop periodically evicts expired connections and refills idle slots.
func (p *connPool) maintainLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.done:
			return
		case <-ticker.C:
			p.evict()
			p.refill()
		}
	}
}

func (p *connPool) evict() {
	p.mu.Lock()
	live := p.idle[:0]
	for _, c := range p.idle {
		if c.expired() {
			c.conn.Close()
			p.total--
		} else {
			live = append(live, c)
		}
	}
	p.idle = live
	p.mu.Unlock()
}

func (p *connPool) refill() {
	p.mu.Lock()
	need := poolMinIdle - len(p.idle)
	p.mu.Unlock()
	for i := 0; i < need; i++ {
		if c := p.dial(); c != nil {
			p.mu.Lock()
			p.idle = append(p.idle, c)
			p.mu.Unlock()
		}
	}
}

func (p *connPool) close() {
	close(p.done)
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.idle {
		c.conn.Close()
	}
	p.idle = nil
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}
