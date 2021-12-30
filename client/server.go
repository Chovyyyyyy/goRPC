package goRPC

//处理通信过程
import (
	"encoding/json"
	"fmt"
	"goRPC/client/codec"
	"io"
	"log"
	"net"
	"reflect"
	"sync"
)

const MagicNumber = 0x3bef5c

// Option 消息的编解码方式
type Option struct {
	MagicNumber int        //MagicNumber记录这是goRPC请求
	CodecType   codec.Type //客户端可能会选择不同Codec来编码body
}

// Server 代表一个RPC服务器
type Server struct{}

type request struct {
	h            *codec.Header // 请求的请求头
	argv, replyv reflect.Value // 请求的argv和replyv
}

// DefaultOption 默认配置
var DefaultOption = &Option{
	MagicNumber: MagicNumber,
	CodecType:   codec.GobType,
}

// DefaultServer 默认 *Server实例
var DefaultServer = NewServer()

// invalidRequest 发生错误时的响应argv占位符
var invalidRequest = struct{}{}

// NewServer 构造服务器
func NewServer() *Server {
	return &Server{}
}

//Accept 接收监听者上的连接
//并为每个传入连接提供请求
func (server *Server) Accept(lis net.Listener) {
	//while（true）等待socket连接的建立，并开启子协程处理，处理过程交给ServerConn方法
	for {
		conn, err := lis.Accept()
		if err != nil {
			log.Println("rpc server: accept error:", err)
			return
		}
		go server.ServeConn(conn)
	}
}

// Accept 默认的Accept
func Accept(lis net.Listener) { DefaultServer.Accept(lis) }

// ServeConn 在单个连接上运行服务器
// ServeConn 阻塞，为连接提供服务，直到客户端挂起
func (server *Server) ServeConn(conn io.ReadWriteCloser) {
	//结束后关闭连接
	defer func() { _ = conn.Close() }()
	var opt Option
	if err := json.NewDecoder(conn).Decode(&opt); err != nil {
		log.Println("rpc server: options error: ", err)
		return
	}
	if opt.MagicNumber != MagicNumber {
		log.Printf("rpc server: invalid magic number %x", opt.MagicNumber)
		return
	}
	f := codec.NewCodecFuncMap[opt.CodecType]
	if f == nil {
		log.Printf("rpc server: invalid codec type %s", opt.CodecType)
		return
	}
	server.serveCodec(f(conn))
}

//serveCodec 主要包含三个过程
//读取请求 readRequest
//处理请求 handleRequest
//回复请求 sendResponse
func (server *Server) serveCodec(cc codec.Codec) {
	//加锁确保发送一个完整请求
	sending := new(sync.Mutex)
	//一直等待所有请求被处理
	wg := new(sync.WaitGroup)

	for {
		req, err := server.readRequest(cc)
		if err != nil {
			//由于没有回复，所以关闭连接
			if req == nil {
				break
			}
			req.h.Error = err.Error()
			server.sendResponse(cc, req.h, invalidRequest, sending)
			continue
		}
		wg.Add(1)
		go server.handleRequest(cc, req, sending, wg)
	}
	wg.Wait()
	_ = cc.Close()
}

func (server *Server) readRequestHeader(cc codec.Codec) (*codec.Header, error) {
	var h codec.Header
	if err := cc.ReadHeader(&h); err != nil {
		if err != io.EOF && err != io.ErrUnexpectedEOF {
			log.Println("rpc server: read header error:", err)
		}
		return nil, err
	}
	return &h, nil
}

func (server *Server) readRequest(cc codec.Codec) (*request, error) {
	h, err := server.readRequestHeader(cc)
	if err != nil {
		return nil, err
	}
	req := &request{h: h}
	req.argv = reflect.New(reflect.TypeOf(""))
	if err = cc.ReadBody(req.argv.Interface()); err != nil {
		log.Println("rpc server: read argv err:", err)
	}
	return req, nil
}

func (server Server) sendResponse(cc codec.Codec, h *codec.Header, body interface{}, sending *sync.Mutex) {
	sending.Lock()
	defer sending.Unlock()
	if err := cc.Write(h, body); err != nil {
		log.Println("rpc server: write response error:", err)
	}
}

func (server *Server) handleRequest(cc codec.Codec, req *request, sending *sync.Mutex, wg *sync.WaitGroup) {
	//响应registered rpc方法来获得正确replyv
	defer wg.Done()
	log.Println(req.h, req.argv.Elem())
	req.replyv = reflect.ValueOf(fmt.Sprintf("goRPC resp %d", req.h.Seq))
	server.sendResponse(cc, req.h, req.replyv.Interface(), sending)
}
