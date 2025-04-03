package registry

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"
)

var (
	// ErrNoAvailableService 表示没有可用的服务实例
	ErrNoAvailableService = errors.New("没有可用的服务实例")
)

// BalancerType 定义负载均衡器类型
type BalancerType string

const (
	// BalancerRandom 随机负载均衡
	BalancerRandom BalancerType = "random"
	// BalancerRoundRobin 轮询负载均衡
	BalancerRoundRobin BalancerType = "round_robin"
	// BalancerConsistentHash 一致性哈希负载均衡
	BalancerConsistentHash BalancerType = "consistent_hash"
)

// Balancer 是负载均衡器接口
type Balancer interface {
	// Select 从服务实例列表中选择一个实例
	Select(services []ServiceInstance, key string) (ServiceInstance, error)
	// Type 返回负载均衡器类型
	Type() BalancerType
}

// RandomBalancer 随机负载均衡器
type RandomBalancer struct{}

// Select 随机选择一个服务实例
func (b *RandomBalancer) Select(services []ServiceInstance, _ string) (ServiceInstance, error) {
	if len(services) == 0 {
		return ServiceInstance{}, ErrNoAvailableService
	}
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return services[r.Intn(len(services))], nil
}

// Type 返回负载均衡器类型
func (b *RandomBalancer) Type() BalancerType {
	return BalancerRandom
}

// RoundRobinBalancer 轮询负载均衡器
type RoundRobinBalancer struct {
	mu    sync.Mutex
	index map[string]int
}

// NewRoundRobinBalancer 创建一个新的轮询负载均衡器
func NewRoundRobinBalancer() *RoundRobinBalancer {
	return &RoundRobinBalancer{
		index: make(map[string]int),
	}
}

// Select 轮询选择一个服务实例
func (b *RoundRobinBalancer) Select(services []ServiceInstance, serviceName string) (ServiceInstance, error) {
	if len(services) == 0 {
		return ServiceInstance{}, ErrNoAvailableService
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	// 获取当前索引
	index := b.index[serviceName]
	// 选择实例
	instance := services[index]
	// 更新索引
	b.index[serviceName] = (index + 1) % len(services)

	return instance, nil
}

// Type 返回负载均衡器类型
func (b *RoundRobinBalancer) Type() BalancerType {
	return BalancerRoundRobin
}

// Discovery 服务发现
type Discovery struct {
	registry   *Registry                     // 服务注册中心
	balancer   Balancer                      // 负载均衡器
	serviceMu  sync.RWMutex                  // 服务缓存锁
	serviceMap map[string][]ServiceInstance  // 服务缓存
	watches    map[string]context.CancelFunc // 服务监视取消函数
	watchMu    sync.RWMutex                  // 监视锁
	logger     *log.Logger                   // 日志
}

// DiscoveryOptions 服务发现选项
type DiscoveryOptions struct {
	Registry *Registry   // 服务注册中心
	Balancer Balancer    // 负载均衡器
	Logger   *log.Logger // 日志
}

// NewDiscovery 创建一个新的服务发现实例
func NewDiscovery(opts DiscoveryOptions) *Discovery {
	if opts.Balancer == nil {
		opts.Balancer = &RandomBalancer{}
	}
	if opts.Logger == nil {
		opts.Logger = log.Default()
	}

	return &Discovery{
		registry:   opts.Registry,
		balancer:   opts.Balancer,
		serviceMap: make(map[string][]ServiceInstance),
		watches:    make(map[string]context.CancelFunc),
		logger:     opts.Logger,
	}
}

// GetService 获取服务实例
func (d *Discovery) GetService(ctx context.Context, serviceName string) (ServiceInstance, error) {
	// 检查缓存
	d.serviceMu.RLock()
	services, ok := d.serviceMap[serviceName]
	d.serviceMu.RUnlock()

	if !ok || len(services) == 0 {
		// 从注册中心获取服务
		var err error
		services, err = d.registry.GetService(ctx, serviceName)
		if err != nil {
			return ServiceInstance{}, fmt.Errorf("获取服务失败: %w", err)
		}

		if len(services) == 0 {
			return ServiceInstance{}, ErrNoAvailableService
		}

		// 更新缓存
		d.serviceMu.Lock()
		d.serviceMap[serviceName] = services
		d.serviceMu.Unlock()

		// 监视服务变更
		d.watchService(serviceName)
	}

	// 使用负载均衡器选择一个服务实例
	return d.balancer.Select(services, serviceName)
}

// GetAllInstances 获取服务的所有实例
func (d *Discovery) GetAllInstances(ctx context.Context, serviceName string) ([]ServiceInstance, error) {
	// 检查缓存
	d.serviceMu.RLock()
	services, ok := d.serviceMap[serviceName]
	d.serviceMu.RUnlock()

	if !ok || len(services) == 0 {
		// 从注册中心获取服务
		var err error
		services, err = d.registry.GetService(ctx, serviceName)
		if err != nil {
			return nil, fmt.Errorf("获取服务失败: %w", err)
		}

		// 更新缓存
		d.serviceMu.Lock()
		d.serviceMap[serviceName] = services
		d.serviceMu.Unlock()

		// 监视服务变更
		d.watchService(serviceName)
	}

	return services, nil
}

// watchService 监视服务变更
func (d *Discovery) watchService(serviceName string) {
	d.watchMu.Lock()
	defer d.watchMu.Unlock()

	// 检查是否已经在监视
	if _, ok := d.watches[serviceName]; ok {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	d.watches[serviceName] = cancel

	// 启动监视
	go func() {
		watchCh, err := d.registry.WatchService(ctx, serviceName)
		if err != nil {
			d.logger.Printf("监视服务 [%s] 失败: %v", serviceName, err)
			return
		}

		for {
			select {
			case <-ctx.Done():
				return
			case services, ok := <-watchCh:
				if !ok {
					return
				}

				// 更新缓存
				d.serviceMu.Lock()
				d.serviceMap[serviceName] = services
				d.serviceMu.Unlock()

				d.logger.Printf("服务 [%s] 发生变更，当前实例数: %d", serviceName, len(services))
			}
		}
	}()
}

// StopWatch 停止监视服务变更
func (d *Discovery) StopWatch(serviceName string) {
	d.watchMu.Lock()
	defer d.watchMu.Unlock()

	if cancel, ok := d.watches[serviceName]; ok {
		cancel()
		delete(d.watches, serviceName)
		d.logger.Printf("停止监视服务 [%s]", serviceName)
	}
}

// SetBalancer 设置负载均衡器
func (d *Discovery) SetBalancer(balancer Balancer) {
	d.balancer = balancer
	d.logger.Printf("负载均衡器已更改为: %s", balancer.Type())
}

// Close 关闭服务发现
func (d *Discovery) Close() {
	// 停止所有监视
	d.watchMu.Lock()
	for serviceName, cancel := range d.watches {
		cancel()
		d.logger.Printf("停止监视服务 [%s]", serviceName)
	}
	d.watches = make(map[string]context.CancelFunc)
	d.watchMu.Unlock()
}
