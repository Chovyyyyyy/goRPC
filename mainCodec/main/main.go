package main

import (
	"encoding/json"
	"fmt"
	"goRPC/mainCodec"
	"goRPC/mainCodec/codec"
	"log"
	"net"
	"time"
)

// startServer 启动server
func startServer(addr chan string) {
	//选择一个空闲的端口
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		//之后会直接调用exit(1)
		log.Fatal("network error:", err)
	}
	log.Println("start rpc server on", l.Addr())
	addr <- l.Addr().String()
	mainCodec.Accept(l)
}

func main() {
	addr := make(chan string)
	//启动一个协程
	go startServer(addr)

	//实现一个简单的goRPC客户端
	conn, _ := net.Dial("tcp", <-addr)
	defer func() { _ = conn.Close() }()

	//休眠一秒
	time.Sleep(time.Second)
	//初始化配置
	_ = json.NewEncoder(conn).Encode(mainCodec.DefaultOption)
	cc := codec.NewGobCodec(conn)
	//设置4个服务器，发送请求和接收响应
	for i := 0; i < 5; i++ {
		h := &codec.Header{
			ServiceMethod: "Foo.Sum",
			Seq:           uint64(i),
		}
		_ = cc.Write(h, fmt.Sprintf("goRPC req %d", h.Seq))
		_ = cc.ReadHeader(h)
		var reply string
		_ = cc.ReadBody(&reply)
		log.Println("reply:", reply)
	}
}
