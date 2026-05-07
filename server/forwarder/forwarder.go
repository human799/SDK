// Package forwarder implements TLS termination + obfs unwrapping + TCP forwarding.
package forwarder

import (
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"proxy-system/server/obfs"
)

// Forwarder manages all forwarding connections.
type Forwarder struct {
	target  string // upstream address host:port
	tlsCfg  *tls.Config
	mu      sync.Mutex
	conns   map[net.Conn]struct{}
	done    chan struct{}
	closed  bool
}

// Config holds Forwarder options.
type Config struct {
	Target   string
	CertFile string // PEM cert file path
	KeyFile  string // PEM key file path
}

// New creates a new Forwarder. If cfg.CertFile is non-empty, TLS is enabled.
func New(cfg Config) (*Forwarder, error) {
	f := &Forwarder{
		target: cfg.Target,
		conns:  make(map[net.Conn]struct{}),
		done:   make(chan struct{}),
	}

	if cfg.CertFile != "" && cfg.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load TLS cert: %w", err)
		}
		f.tlsCfg = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
		log.Printf("[Forwarder] TLS enabled, cert=%s", cfg.CertFile)
	}

	return f, nil
}

// WrapTLS upgrades a raw TCP listener connection to TLS if configured.
// Returns the (possibly wrapped) conn and whether obfs should be applied.
func (f *Forwarder) WrapTLS(raw net.Conn) (net.Conn, bool) {
	if f.tlsCfg == nil {
		return raw, false
	}
	return tls.Server(raw, f.tlsCfg), true
}

// Done returns the close signal channel.
func (f *Forwarder) Done() <-chan struct{} {
	return f.done
}

// Close shuts down all active connections.
func (f *Forwarder) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	f.closed = true
	close(f.done)
	for c := range f.conns {
		c.Close()
	}
}

func (f *Forwarder) addConn(c net.Conn) {
	f.mu.Lock()
	f.conns[c] = struct{}{}
	f.mu.Unlock()
}

func (f *Forwarder) removeConn(c net.Conn) {
	f.mu.Lock()
	delete(f.conns, c)
	f.mu.Unlock()
}

// Handle processes one inbound connection:
//  1. TLS handshake (if configured)
//  2. Unwrap obfs framing (pipe mode: decode frames, stream to upstream)
//  3. Forward to upstream with PROXY Protocol v1
func (f *Forwarder) Handle(raw net.Conn) {
	f.addConn(raw)
	defer func() {
		f.removeConn(raw)
		raw.Close()
	}()

	client, useObfs := f.WrapTLS(raw)
	if client != raw {
		f.addConn(client)
		defer func() {
			f.removeConn(client)
			client.Close()
		}()
	}

	remote, err := net.DialTimeout("tcp", f.target, 10*time.Second)
	if err != nil {
		log.Printf("[Forwarder] upstream connect failed %s: %v", f.target, err)
		return
	}
	defer remote.Close()
	f.addConn(remote)
	defer f.removeConn(remote)

	log.Printf("[Forwarder] %s -> %s (obfs=%v)", raw.RemoteAddr(), f.target, useObfs)

	// Inject PROXY Protocol v1 so upstream sees real client IP
	if err := sendProxyProtocolV1(remote, raw.RemoteAddr(), remote.LocalAddr()); err != nil {
		log.Printf("[Forwarder] PPv1 failed: %v", err)
		return
	}

	if useObfs {
		// obfs mode: decode frames client→remote, encode frames remote→client
		obfsClient := obfs.New(client)
		relayObfs(obfsClient, remote)
	} else {
		relay(client, remote)
	}
}

// relayObfs pipes between an obfs connection and a plain TCP connection.
// client→remote: read full obfs frames, write raw payload to remote
// remote→client: read raw bytes from remote, write obfs frames to client
func relayObfs(client *obfs.Conn, remote net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	// client → remote: decode obfs frames, forward raw payload
	go func() {
		defer wg.Done()
		buf := make([]byte, obfs.MaxFrame)
		for {
			n, err := client.Read(buf)
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

	// remote → client: read raw bytes, encode into obfs frames
	go func() {
		defer wg.Done()
		buf := make([]byte, obfs.MaxFrame)
		for {
			n, err := remote.Read(buf)
			if n > 0 {
				if _, werr := client.Write(buf[:n]); werr != nil {
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

func sendProxyProtocolV1(dst net.Conn, clientAddr, serverAddr net.Addr) error {
	clientTCP := clientAddr.(*net.TCPAddr)
	serverTCP := serverAddr.(*net.TCPAddr)

	proto := "TCP4"
	if clientTCP.IP.To4() == nil {
		proto = "TCP6"
	}

	header := fmt.Sprintf("PROXY %s %s %s %d %d\r\n",
		proto,
		clientTCP.IP.String(),
		serverTCP.IP.String(),
		clientTCP.Port,
		serverTCP.Port,
	)

	_, err := fmt.Fprint(dst, header)
	return err
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
