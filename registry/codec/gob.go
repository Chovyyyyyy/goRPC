package registry

import (
	"bufio"
	"encoding/gob"
	"io"
	"log"
)

// GobCodec GobCodec结构体
type GobCodec struct {
	conn io.ReadWriteCloser //通过TCP或UNIX建立socket时得到的链接实例
	buf  *bufio.Writer      //为了防止阻塞而创建的带缓冲的Writer，提升性能
	dec  *gob.Decoder       //gob的译码器
	enc  *gob.Encoder       //gob的编码器
}

// 目的是为了确保接口被实现调用。即利用强制类型转换，确保struct GobCodec实现了接口Codec。这样IDE和编译期间就可以检查，而不是等到使用的时候
var _ Codec = (*GobCodec)(nil)

// Close 实现连接关闭
func (g *GobCodec) Close() error {
	return g.conn.Close()
}

// ReadHeader 读取请求头
func (g *GobCodec) ReadHeader(h *Header) error {
	return g.dec.Decode(h)
}

// ReadBody 读取请求体
func (g *GobCodec) ReadBody(body interface{}) error {
	return g.dec.Decode(body)
}

func (g GobCodec) Write(h *Header, body interface{}) (err error) {
	defer func() {
		_ = g.buf.Flush()
		if err != nil {
			_ = g.Close()
		}
	}()
	if err := g.enc.Encode(h); err != nil {
		log.Println("rpc mainCodec: gob error encoding header:", err)
		return err
	}
	if err := g.enc.Encode(body); err != nil {
		log.Println("rpc mainCodec: gob error encoding body:", err)
		return err
	}

	return nil

}

func NewGobCodec(conn io.ReadWriteCloser) Codec {
	buf := bufio.NewWriter(conn)
	return &GobCodec{
		conn: conn,
		buf:  buf,
		dec:  gob.NewDecoder(conn),
		enc:  gob.NewEncoder(buf),
	}
}
