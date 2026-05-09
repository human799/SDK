package main

import (
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"proxy-system/server/forwarder"
)

func main() {
	listenAddr := flag.String("listen", "0.0.0.0:443", "监听地址 host:port")
	targetAddr := flag.String("target", "52.128.229.186:10443", "源站地址 host:port")
	certFile := flag.String("cert", "", "TLS 证书 PEM 路径（留空则明文）")
	keyFile := flag.String("key", "", "TLS 私钥 PEM 路径")
	flag.Parse()

	if *targetAddr == "" {
		log.Fatal("[Server] 必须指定 -target 源站地址")
	}

	fwd, err := forwarder.New(forwarder.Config{
		Target:   *targetAddr,
		CertFile: *certFile,
		KeyFile:  *keyFile,
	})
	if err != nil {
		log.Fatalf("[Server] 初始化失败: %v", err)
	}

	log.Printf("[Server] 启动，监听 %s -> 转发至 %s", *listenAddr, *targetAddr)

	ln, err := net.Listen("tcp", *listenAddr)
	if err != nil {
		log.Fatalf("[Server] 监听失败: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-fwd.Done():
					return
				default:
					log.Printf("[Server] Accept错误: %v", err)
					continue
				}
			}
			go fwd.Handle(conn)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("[Server] 正在关闭...")
	fwd.Close()
}
