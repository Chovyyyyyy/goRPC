package httpDebug

import (
	"io"
)

// Header 请求头
type Header struct {
	ServiceMethod string // 服务名和方法名：通常与Go中的结构体和方法互相映射
	Seq           uint64 // 请求序号：也可以认为是某个请求的ID，用来区分不同的请求
	Error         string // 错误信息：客户端置为空，服务端如果发生错误，将错误信息置于Error中
}

// Codec 对消息体进行编解码的接口
type Codec interface {
	io.Closer
	ReadHeader(*Header) error
	ReadBody(interface{}) error
	Write(*Header, interface{}) error
}

// NewCodecFun Codec的构造函数
type NewCodecFun func(closer io.ReadWriteCloser) Codec

// Type 类别
type Type string

const (
	GobType  Type = "application/gob"
	JsonType Type = "application/json"
)

// NewCodecFuncMap NewCodecFuncMao 类别和构造方法之间的映射
var NewCodecFuncMap map[Type]NewCodecFun

func init() {
	NewCodecFuncMap = make(map[Type]NewCodecFun)
	NewCodecFuncMap[GobType] = NewGobCodec
}
