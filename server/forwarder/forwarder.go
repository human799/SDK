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

// decoyRedirectURL is where plain HTTP/non-TLS probes are redirected.
const decoyRedirectURL = "https://www.baidu.com"

// Forwarder manages all forwarding connections.
type Forwarder struct {
	target string // upstream address host:port
	tlsCfg *tls.Config
	mu     sync.Mutex
	conns  map[net.Conn]struct{}
	done   chan struct{}
	closed bool
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
//  1. Sniff first byte: non-TLS traffic (browsers/scanners) gets a decoy HTTP redirect
//  2. TLS handshake (if configured)
//  3. Sniff first byte inside TLS: non-obfs gets a decoy HTTP redirect
//  4. Unwrap obfs framing → forward to upstream with PROXY Protocol v1
func (f *Forwarder) Handle(raw net.Conn) {
	f.addConn(raw)
	defer func() {
		f.removeConn(raw)
		raw.Close()
	}()

	// Peek exactly 1 byte without buffering extras.
	// TLS ClientHello always starts with 0x16 (content type: handshake).
	oneByte := make([]byte, 1)
	if _, err := io.ReadFull(raw, oneByte); err != nil {
		return
	}

	// Restore the peeked byte.
	pconn := &peekedConn{Conn: raw, Reader: io.MultiReader(newByteReader(oneByte[0]), raw)}

	if oneByte[0] != 0x16 {
		log.Printf("[Forwarder] non-TLS probe from %s → decoy redirect", raw.RemoteAddr())
		sendDecoyRedirect(pconn)
		return
	}

	// TLS path
	var client net.Conn = pconn
	useObfs := false
	if f.tlsCfg != nil {
		tlsConn := tls.Server(pconn, f.tlsCfg)
		f.addConn(tlsConn)
		defer func() {
			f.removeConn(tlsConn)
			tlsConn.Close()
		}()

		if err := tlsConn.Handshake(); err != nil {
			log.Printf("[Forwarder] TLS handshake failed from %s: %v", raw.RemoteAddr(), err)
			return
		}

		// Peek exactly 1 byte inside TLS.
		// obfs magic starts with 0xDE; browser HTTP starts with 'G','P','H', etc.
		if _, err := io.ReadFull(tlsConn, oneByte); err != nil {
			return
		}

		if oneByte[0] != 0xDE {
			log.Printf("[Forwarder] non-obfs HTTPS probe from %s → decoy redirect", raw.RemoteAddr())
			innerConn := &peekedConn{Conn: tlsConn, Reader: io.MultiReader(newByteReader(oneByte[0]), tlsConn)}
			sendDecoyRedirect(innerConn)
			return
		}

		// Put the 0xDE byte back for obfs decoder.
		client = &peekedConn{Conn: tlsConn, Reader: io.MultiReader(newByteReader(oneByte[0]), tlsConn)}
		useObfs = true
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

	if err := sendProxyProtocolV1(remote, raw.RemoteAddr(), remote.LocalAddr()); err != nil {
		log.Printf("[Forwarder] PPv1 failed: %v", err)
		return
	}

	if useObfs {
		// Use NewWithReader so the peeked 0xDE byte is not lost.
		pc := client.(*peekedConn)
		relayObfs(obfs.NewWithReader(pc.Conn, pc.Reader), remote)
	} else {
		relay(client, remote)
	}
}

// peekedConn wraps net.Conn with a pre-buffered reader after peeking bytes.
type peekedConn struct {
	net.Conn
	Reader io.Reader
}

func (c *peekedConn) Read(b []byte) (int, error) { return c.Reader.Read(b) }

// newByteReader returns an io.Reader that yields exactly one byte.
func newByteReader(b byte) io.Reader {
	return &singleByteReader{b: b, done: false}
}

type singleByteReader struct {
	b    byte
	done bool
}

func (r *singleByteReader) Read(p []byte) (int, error) {
	if r.done || len(p) == 0 {
		return 0, io.EOF
	}
	p[0] = r.b
	r.done = true
	return 1, nil
}

// sendDecoyRedirect writes a minimal HTTP 302 response redirecting to decoyRedirectURL.
func sendDecoyRedirect(c net.Conn) {
	resp := "HTTP/1.1 302 Found\r\n" +
		"Location: " + decoyRedirectURL + "\r\n" +
		"Content-Length: 0\r\n" +
		"Connection: close\r\n" +
		"\r\n"
	c.Write([]byte(resp)) //nolint:errcheck
}

// relayObfs pipes between an obfs connection and a plain TCP connection.
func relayObfs(client *obfs.Conn, remote net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

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
