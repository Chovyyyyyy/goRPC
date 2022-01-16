package xclient

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"time"
)

type SelectMode int

const (
	RandomSelect     SelectMode = iota // 使用随机算法
	RoundRobinSelect                   // 使用轮询算法
)

type Discovery interface {
	Refresh() error // 从远程注册表更新
	Update(servers []string) error
	Get(mode SelectMode) (string, error)
	GetAll() ([]string, error)
}

// MultiServersDiscovery MultiServersDiscovery是一个不需要注册中心的多服务发现
// 用户提供显式服务器地址
type MultiServersDiscovery struct {
	r       *rand.Rand
	mu      sync.RWMutex
	servers []string
	index   int // 记录轮询算法的位置
}

// NewMultiServerDiscovery 创建NewMultiServerDiscovery实例
func NewMultiServerDiscovery(servers []string) *MultiServersDiscovery {
	d := &MultiServersDiscovery{
		servers: servers,
		r:       rand.New(rand.NewSource(time.Now().UnixNano())), // 产生随机数的实例，初始化时使用时间戳设定随机数种子，避免每次产生相同的随机数序列
	}
	d.index = d.r.Intn(math.MaxInt32 - 1) // 记录Round Robin 算法已经轮循到的位置，为了避免每次从零开始，初始化时随机设定一个值
	return d
}

var _ Discovery = (*MultiServersDiscovery)(nil)

// Refresh 刷新对多服务器发现无效，可以忽略
func (d *MultiServersDiscovery) Refresh() error {
	return nil
}

// Update 需要时，动态更新发现的服务器
func (d *MultiServersDiscovery) Update(servers []string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.servers = servers
	return nil
}

// Get 根据模式获取一个服务器
func (d *MultiServersDiscovery) Get(mode SelectMode) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := len(d.servers)
	if n == 0 {
		return "", errors.New("rpc discovery: no available servers")
	}
	switch mode {
	case RandomSelect:
		return d.servers[d.r.Intn(n)], nil
	case RoundRobinSelect:
		s := d.servers[d.index%n] // 服务器可以更新，所以模式n确保安全
		d.index = (d.index + 1) % n
		return s, nil
	default:
		return "", errors.New("rpc discovery: not supported select mode")
	}
}

// GetAll 返回发现的所有服务器
func (d *MultiServersDiscovery) GetAll() ([]string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	// 返回一个服务器复制版
	servers := make([]string, len(d.servers), len(d.servers))
	copy(servers, d.servers)
	return servers, nil
}
