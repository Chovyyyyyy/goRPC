package main

import (
	"fmt"
	"goRPC/client"
	"log"
	"net"
	"sync"
	"time"
)

func startServer(addr chan string) {
	//获取一个空闲端口
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		log.Fatal("network error:", err)
	}
	log.Println("start rpc server on",l.Addr())
	addr <- l.Addr().String()
	goRPC.Accept(l)
}

func main() {
	log.SetFlags(0)
	addr := make(chan string)
	go startServer(addr)
	client, _ := goRPC.Dial("tcp", <-addr)
	defer func() {_ = client.Close()}()

	time.Sleep(time.Second)
	//发送请求和接收响应
	var wg sync.WaitGroup
	for i:= 0; i<5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			args := fmt.Sprintf("goRPC req %d", i)
			var reply string
			if err := client.Call("Foo.Sum",args,&reply); err != nil {
				log.Fatal("call Foo.Sum error:",err)
			}
			log.Println("reply:",reply)
		}(i)
	}
	wg.Wait()
}
