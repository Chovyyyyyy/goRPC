package service

//处理通信过程
import (
	"encoding/json"
	"errors"
	"goRPC/service/codec"
	"io"
	"log"
	"net"
	"reflect"
	"strings"
	"sync"
)

const MagicNumber = 0x3bef5c

// Option 消息的编解码方式
type Option struct {
	MagicNumber int        //MagicNumber记录这是goRPC请求
	CodecType   codec.Type //客户端可能会选择不同Codec来编码body
}

// Server 代表一个RPC服务器
type Server struct {
	serviceMap sync.Map
}

type request struct {
	h            *codec.Header // 请求的请求头
	argv, replyv reflect.Value // 请求的argv和replyv
	mtype        *methodType
	svc          *service
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

// readRequest 通过newArgv()和newReplyv()两个方法创建出两个入参实例
// 通过cc.ReadBody()将请求报文反序列化为第一个入参argv
func (server *Server) readRequest(cc codec.Codec) (*request, error) {
	h, err := server.readRequestHeader(cc)
	if err != nil {
		return nil, err
	}
	req := &request{h: h}
	req.svc, req.mtype, err = server.findService(h.ServiceMethod)
	if err != nil {
		return req, err
	}
	req.argv = req.mtype.newArgv()
	req.replyv = req.mtype.newReplyv()
	//确保argvi是一个指针，ReadBody需要指针作为参数
	argvi := req.argv.Interface()
	if req.argv.Type().Kind() != reflect.Ptr {
		argvi = req.argv.Addr().Interface()
	}
	if err = cc.ReadBody(argvi); err != nil {
		log.Println("rpc server: read body err:", err)
		return req, err
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

// handleRequest 通过req.svc.call完成方法调用，将replyv传递给sendResponse完成序列化即可
func (server *Server) handleRequest(cc codec.Codec, req *request, sending *sync.Mutex, wg *sync.WaitGroup) {
	//响应registered rpc方法来获得正确replyv
	defer wg.Done()
	err := req.svc.call(req.mtype,req.argv,req.replyv)
	if err != nil {
		req.h.Error = err.Error()
		server.sendResponse(cc,req.h,invalidRequest,sending)
		return
	}
	server.sendResponse(cc, req.h, req.replyv.Interface(), sending)
}

// Register 注册在服务器中发布的方法
func (server *Server) Register(rcvr interface{}) error {
	s := newService(rcvr)
	if _, dup := server.serviceMap.LoadOrStore(s.name, s); dup {
		return errors.New("rpc: service already defined: " + s.name)
	}
	return nil
}

// Register 在默认服务端注册发布接受者的方法
func Register(rcvr interface{}) error {
	return DefaultServer.Register(rcvr)
}

// findService
// 因为ServiceMethod是由Service和Method构成的
// 首先在serviceMap中找到对应的service实例
//再从service实例的method中，找到对应的methodType
func (server *Server) findService(serviceMethod string) (svc *service, mtype *methodType, err error) {
	dot := strings.LastIndex(serviceMethod, ".")
	if dot < 0 {
		err = errors.New("rpc server: service/method request ill-formed: " + serviceMethod)
		return
	}
	serviceName, methodName := serviceMethod[:dot], serviceMethod[dot+1:]
	svci, ok := server.serviceMap.Load(serviceName)
	if !ok {
		err = errors.New("rpc server: can't find service" + serviceName)
		return
	}
	svc = svci.(*service)
	mtype = svc.method[methodName]
	if mtype == nil {
		err = errors.New("rpc server: can't find method " + methodName)
	}
	return
}
