// Package forwarder 实现TCP四层流量转发
// 所有入站连接统一转发到固定的目标地址（源站）
package forwarder

import (
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

// Forwarder 管理所有转发连接
type Forwarder struct {
	target string // 源站地址 host:port
	mu     sync.Mutex
	conns  map[net.Conn]struct{}
	done   chan struct{}
	closed bool
}

// New 创建新的Forwarder，target 为源站地址，格式 host:port
func New(target string) *Forwarder {
	return &Forwarder{
		target: target,
		conns:  make(map[net.Conn]struct{}),
		done:   make(chan struct{}),
	}
}

// Done 返回关闭信号channel
func (f *Forwarder) Done() <-chan struct{} {
	return f.done
}

// Close 关闭所有连接
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

// Handle 处理一个客户端连接，直接转发到源站并注入 PPv1 头
func (f *Forwarder) Handle(client net.Conn) {
	f.addConn(client)
	defer func() {
		f.removeConn(client)
		client.Close()
	}()

	remote, err := net.DialTimeout("tcp", f.target, 10*time.Second)
	if err != nil {
		log.Printf("[Forwarder] 连接源站失败 %s: %v", f.target, err)
		return
	}
	defer remote.Close()
	f.addConn(remote)
	defer f.removeConn(remote)

	log.Printf("[Forwarder] %s -> %s", client.RemoteAddr(), f.target)

	// 发送 PROXY Protocol v1，透传客户端真实 IP
	if err := sendProxyProtocolV1(remote, client.RemoteAddr(), remote.LocalAddr()); err != nil {
		log.Printf("[Forwarder] 发送PPv1失败: %v", err)
		return
	}

	relay(client, remote)
}

// sendProxyProtocolV1 向目标连接发送 PROXY Protocol v1 头
// 格式: "PROXY TCP4 <客户端IP> <服务器出口IP> <客户端端口> <服务器端口>\r\n"
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

// relay 双向转发数据
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
