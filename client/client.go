package goRPC

import (
	"encoding/json"
	"errors"
	"fmt"
	"goRPC/client/codec"
	"io"
	"log"
	"net"
	"sync"
)

// Call 封装一次RPC调用所需要的信息
type Call struct {
	Seq           uint64
	ServiceMethod string      // 格式 "<service>.<method>"
	Args          interface{} // function的参数
	Reply         interface{} // function的响应
	Error         error       // 如果错误发生，会被初始化
	Done          chan *Call  // 当调用结束时，会调用call.Done()通知调用方
}

type Client struct {
	cc      codec.Codec // 消息的编译码器，和服务端类似，用来序列化将要发送出去的请求，以及反序列化接收到的响应
	opt     *Option
	sending sync.Mutex   // 互斥锁，和服务端类似，为了保证请求有序发送，防止多个请求报文混淆
	header  codec.Header // 每个消息的请求头，header只有在请求发送时才需要，而发送请求是互斥的，因此每个客户端只需要一个，声明在Client结构体中可以复用
	mu      sync.Mutex
	seq     uint64           // 用于给发送的请求编号，每个请求拥有唯一编号
	pending map[uint64]*Call // 存储未处理完的请求，key：value=编号：call实例
	// 任意一个值true，都表示Client不可用
	closing  bool // 一般是用户主动关闭
	shutdown bool // 一般有错误发生

}

var _ io.Closer = (*Client)(nil)

var ErrShutdown = errors.New("connecting is shut down")

func (call *Call) done() {
	call.Done <- call
}

// Close 关闭连接
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing {
		return ErrShutdown
	}
	c.closing = true
	return c.cc.Close()
}

// IsAvailable 返回client是否可用
func (c *Client) IsAvailable() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.shutdown && !c.closing
}

//registerCall 将参数call添加到pending中，并更新seq
func (c *Client) registerCall(call *Call) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing || c.shutdown {
		return 0, ErrShutdown
	}
	call.Seq = c.seq
	c.pending[call.Seq] = call
	c.seq++
	return call.Seq, nil
}

//根据seq，从pending移除对应的call，并返回
func (c *Client) removeCall(seq uint64) *Call {
	c.mu.Lock()
	defer c.mu.Unlock()
	call := c.pending[seq]
	delete(c.pending, seq)
	return call
}

// terminateCalls 当服务端或者客户端发生错误时调用，将shutdown设置为true，并且将错误消息通知所有pending状态的call
func (c *Client) terminateCalls(err error) {
	c.sending.Lock()
	defer c.sending.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.shutdown = true
	for _, call := range c.pending {
		call.Error = err
		call.done()
	}
}

// receive
// call不存在，可能是请求没有发送完整，或者因为其他原因被取消，但是但是服务端依旧处理
// call存在，但服务端处理出错，即h.Error不为空
// call存在，服务端处理正常，那么需要从body中读取Reply的值
func (c *Client) receive() {
	var err error
	for err == nil {
		var h codec.Header
		if err = c.cc.ReadHeader(&h); err != nil {
			break
		}
		call := c.removeCall(h.Seq)
		switch {
		case call == nil:
			//写入操作失败，并且移除调用
			err = c.cc.ReadBody(nil)
		case h.Error != "":
			call.Error = fmt.Errorf(h.Error)
			err = c.cc.ReadBody(nil)
			call.done()
		default:
			err = c.cc.ReadBody(call.Reply)
			if err != nil {
				call.Error = errors.New("reading body " + err.Error())
			}
			call.done()
		}
	}
	//发生错误时，terminateCalls挂起调用
	c.terminateCalls(err)
}

// send 发送请求
func (c *Client) send(call *Call) {
	//确保客户端会发送完整请求
	c.sending.Lock()
	defer c.sending.Unlock()

	//注册这个调用
	seq, err := c.registerCall(call)
	if err != nil {
		call.Error = err
		call.done()
		return
	}

	//准备请求头
	c.header.ServiceMethod = call.ServiceMethod
	c.header.Seq = seq
	c.header.Error = ""

	//编码和发送请求
	if err := c.cc.Write(&c.header, call.Args); err != nil {
		call := c.removeCall(seq)
		//调用可能为空，这通常意味着写失败
		//服务端已经接收请求和处理
		if call != nil {
			call.Error = err
			call.done()
		}
	}
}

// Go 异步接口，返回Call实例
func (c *Client) Go(serviceMethod string, args, reply interface{}, done chan *Call) *Call {
	if done == nil {
		done = make(chan *Call, 10)
	} else if cap(done) == 0 {
		log.Panic("rpc client: done channel is unbuffered")
	}
	call := &Call{
		ServiceMethod: serviceMethod,
		Args:          args,
		Reply:         reply,
		Done:          done,
	}
	c.send(call)
	return call
}

// Call 对Go的封装，阻塞call.Done，等待响应返回，是一个同步接口
func (c *Client)Call(serviceMethod string, args, reply interface{}) error {
	call := <-c.Go(serviceMethod, args, reply, make(chan *Call, 1)).Done
	return call.Error
}

func parseOptions(opts ...*Option) (*Option, error) {
	//如果opts为空或者以nil为参数
	if len(opts) == 0 || opts[0] == nil {
		return DefaultOption, nil
	}
	if len(opts) != 1 {
		return nil, errors.New("number of options is more than 1")
	}
	opt := opts[0]
	opt.MagicNumber = DefaultOption.MagicNumber
	if opt.CodecType == "" {
		opt.CodecType = DefaultOption.CodecType
	}
	return opt, nil
}


// NewClient 创建Client实例，首先需要一开始的协议交换，即发送Option信息给服务端
func NewClient(conn net.Conn, opt *Option) (*Client, error) {
	f := codec.NewCodecFuncMap[opt.CodecType]
	if f == nil {
		err := fmt.Errorf("invalid codec type %s", opt.CodecType)
		log.Println("rpc client: codec error:", err)
		return nil, err
	}
	// 发送option给服务端
	if err := json.NewEncoder(conn).Encode(opt); err != nil {
		log.Println("rpc client: options error: ", err)
		_ = conn.Close()
		return nil, err
	}
	return newClientCodec(f(conn), opt), nil
}

//协商好消息的编译码方式之后，再创建一个子协程调用receive()接收响应
func newClientCodec(cc codec.Codec, opt *Option) *Client {
	client := &Client{
		seq:     1, //1代表开始，0代表不可用
		cc:      cc,
		opt:     opt,
		pending: make(map[uint64]*Call),
	}
	go client.receive()
	return client
}


// Dial 便于用户传入服务端地址，创建Client实例
func Dial(network, address string, opts ...*Option) (client *Client, err error) {
	opt, err := parseOptions(opts...)
	if err != nil {
		return nil, err
	}
	conn, err := net.Dial(network, address)
	if err != nil {
		return nil, err
	}
	//如果client为空关闭连接
	defer func() {
		if err != nil {
			_ = conn.Close()
		}
	}()
	return NewClient(conn, opt)
}
