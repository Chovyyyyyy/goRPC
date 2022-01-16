package xclient

import (
	"context"
	"goRPC/loadBalance"
	"io"
	"reflect"
	"sync"
)


type XClient struct {
	d    Discovery
	mode SelectMode
	opt  *loadBalance.Option
	mu sync.Mutex
	clients map[string]*loadBalance.Client
}


var _ io.Closer = (*XClient)(nil)

func (xc *XClient) Close() error {
	xc.mu.Lock()
	defer xc.mu.Unlock()
	for key,client := range xc.clients {
		//忽略错误
		_ = client.Close()
		delete(xc.clients,key)
	}
	return nil
}

func NewXClient(d Discovery,mode SelectMode,opt *loadBalance.Option) *XClient {
	return &XClient{d: d,mode: mode,opt: opt,clients: make(map[string]*loadBalance.Client)}
}

func (xc *XClient) dial(rpcAddr string) (*loadBalance.Client,error) {
	xc.mu.Lock()
	defer xc.mu.Unlock()
	client, ok := xc.clients[rpcAddr]
	if ok && !client.IsAvailable() {
		_ = client.Close()
		delete(xc.clients,rpcAddr)
		client = nil
	}
	if client == nil {
		var err error
		client,err = loadBalance.XDial(rpcAddr,xc.opt)
		if err != nil {
			return nil,err
		}
		xc.clients[rpcAddr] = client
	}
	return client,nil
}

func (xc *XClient) call(rpcAddr string,ctx context.Context,serviceMethod string,args,reply interface{}) error {
	client,err := xc.dial(rpcAddr)
	if err != nil {
		return err
	}
	return client.Call(ctx,serviceMethod,args,reply)
}

func (xc *XClient) Call(ctx context.Context, serviceMethod string, args, reply interface{}) error {
	rpcAddr, err := xc.d.Get(xc.mode)
	if err != nil {
		return err
	}
	return xc.call(rpcAddr,ctx,serviceMethod,args,reply)
}

// Broadcast 广播为发现中所有注册的服务器调用命名函数
func (xc *XClient) Broadcast(ctx context.Context,serviceMethod string,args,reply interface{}) error {
	servers,err := xc.d.GetAll()
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var e error
	replyDone := reply == nil
	ctx,cancel := context.WithCancel(ctx)
	for _,rpcAddr := range servers {
		wg.Add(1)
		go func(rpcAddr string) {
			defer wg.Done()
			var clonedReply interface{}
			if reply != nil {
				clonedReply = reflect.New(reflect.ValueOf(reply).Elem().Type()).Interface()
			}
			err := xc.call(rpcAddr,ctx,serviceMethod,args,clonedReply)
			mu.Lock()
			if err != nil && e == nil {
				e = err
				cancel()
			}
			if err == nil && !replyDone {
				reflect.ValueOf(reply).Elem().Set(reflect.ValueOf(clonedReply).Elem())
				replyDone = true
			}
			mu.Unlock()
		}(rpcAddr)
	}
	wg.Wait()
	return e
}