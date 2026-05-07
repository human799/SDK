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
	listenAddr := flag.String("listen", "0.0.0.0:10443", "监听地址，格式: host:port")
	targetAddr := flag.String("target", "52.128.229.186:10443", "源站地址，格式: host:port（必填）")
	flag.Parse()

	if *targetAddr == "" {
		log.Fatal("[Server] 必须指定 -target 源站地址，例如: -target 1.2.3.4:443")
	}

	log.Printf("[Server] 启动，监听 %s -> 转发至 %s", *listenAddr, *targetAddr)

	ln, err := net.Listen("tcp", *listenAddr)
	if err != nil {
		log.Fatalf("[Server] 监听失败: %v", err)
	}
	defer ln.Close()

	fwd := forwarder.New(*targetAddr)

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
