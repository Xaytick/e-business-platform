// Package registry 提供基于etcd的服务注册与发现功能
package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

const (
	DefaultRootPath    = "microservices/registry"
	DefaultRegisterTTL = 15 * time.Second
	DefaultDialTimeout = 5 * time.Second
)

type ServiceInstance struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Version   string            `json:"version"`
	Metadata  map[string]string `json:"metadata"`
	Endpoints []string          `json:"endpoints"`
	Status    string            `json:"status"`
}

type Options struct {
	EtcdConfig  clientv3.Config
	TTL         time.Duration
	DialTimeout time.Duration
	RootPath    string
	Logger      *log.Logger
}

type Registry struct {
	opts      Options
	client    *clientv3.Client
	leaseID   clientv3.LeaseID
	serviceMu sync.RWMutex
	services  map[string]ServiceInstance
	ctx       context.Context
	cancel    context.CancelFunc
}

func DefaultOptions() Options {
	return Options{
		EtcdConfig: clientv3.Config{
			Endpoints:   []string{"localhost:2379"},
			DialTimeout: DefaultDialTimeout,
		},
		TTL:         DefaultRegisterTTL,
		DialTimeout: DefaultDialTimeout,
		RootPath:    DefaultRootPath,
		Logger:      log.Default(),
	}
}

func NewRegistry(opts Options) (*Registry, error) {
	if opts.Logger == nil {
		opts.Logger = log.Default()
	}

	if opts.RootPath == "" {
		opts.RootPath = DefaultRootPath
	}

	if opts.TTL == 0 {
		opts.TTL = DefaultRegisterTTL
	}

	client, err := clientv3.New(opts.EtcdConfig)
	if err != nil {
		return nil, fmt.Errorf("创建etcd客户端失败: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &Registry{
		opts:     opts,
		client:   client,
		services: make(map[string]ServiceInstance),
		ctx:      ctx,
		cancel:   cancel,
	}, nil
}

// Register 注册服务实例到etcd
func (r *Registry) Register(ctx context.Context, service ServiceInstance) error {
	// 验证服务实例
	if service.Name == "" {
		return fmt.Errorf("服务名称不能为空")
	}
	if len(service.Endpoints) == 0 {
		return fmt.Errorf("服务实例必须至少有一个端点")
	}
	if service.ID == "" {
		return fmt.Errorf("服务实例ID不能为空")
	}

	// 创建租约
	leaseResp, err := r.client.Grant(ctx, int64(r.opts.TTL.Seconds()))
	if err != nil {
		return fmt.Errorf("创建租约失败: %w", err)
	}
	r.leaseID = leaseResp.ID

	// 将服务实例信息序列化为JSON
	serviceKey := path.Join(r.opts.RootPath, service.Name, service.ID)
	serviceValue, err := json.Marshal(service)
	if err != nil {
		return fmt.Errorf("序列化服务实例失败: %w", err)
	}

	// 写入etcd
	_, err = r.client.Put(ctx, serviceKey, string(serviceValue), clientv3.WithLease(leaseResp.ID))
	if err != nil {
		return fmt.Errorf("注册服务失败: %w", err)
	}

	// 保存服务实例
	r.serviceMu.Lock()
	r.services[service.ID] = service
	r.serviceMu.Unlock()

	// 心跳保持
	go r.keepAlive(service)

	r.opts.Logger.Printf("服务实例[%s-%s]注册成功，服务端点：%v", service.Name, service.ID, service.Endpoints)

	return nil
}

// keepAlive 心跳保持
func (r *Registry) keepAlive(service ServiceInstance) {
	// 获取心跳通道
	keepAliveCh, err := r.client.KeepAlive(r.ctx, r.leaseID)
	if err != nil {
		r.opts.Logger.Printf("服务实例[%s-%s]心跳保持失败: %v", service.Name, service.ID, err)
		return
	}
	// 处理心跳响应
	for {
		select {
		case <-r.ctx.Done():
			r.opts.Logger.Printf("服务实例[%s-%s]停止心跳", service.Name, service.ID)
			return
		case _, ok := <-keepAliveCh:
			if !ok {
				r.opts.Logger.Printf("服务实例[%s-%s]心跳通道关闭", service.Name, service.ID)
				// 重新获取心跳通道
				err := r.Register(context.Background(), service)
				if err != nil {
					r.opts.Logger.Printf("服务实例[%s-%s]重新注册失败: %v", service.Name, service.ID, err)
				}
			}
		}
	}
}

// Deregister 注销服务实例
func (r *Registry) Deregister(ctx context.Context, service ServiceInstance) error {
	serviceKey := path.Join(r.opts.RootPath, service.Name, service.ID)
	_, err := r.client.Delete(ctx, serviceKey)
	if err != nil {
		return fmt.Errorf("注销服务失败: %w", err)
	}

	// 从内存中删除服务实例
	r.serviceMu.Lock()
	delete(r.services, service.ID)
	r.serviceMu.Unlock()

	r.opts.Logger.Printf("服务实例[%s-%s]注销成功", service.Name, service.ID)
	return nil
}

// GetService 获取服务实例列表
func (r *Registry) GetService(ctx context.Context, serviceName string) ([]ServiceInstance, error) {
	serviceKey := path.Join(r.opts.RootPath, serviceName)
	resp, err := r.client.Get(ctx, serviceKey, clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("获取服务实例失败: %w", err)
	}

	var instances []ServiceInstance
	for _, kv := range resp.Kvs {
		var instance ServiceInstance
		err := json.Unmarshal(kv.Value, &instance)
		if err != nil {
			r.opts.Logger.Printf("解析服务实例失败: %v", err)
			continue
		}
		instances = append(instances, instance)
	}

	return instances, nil
}

// WatchService 监听服务实例变化
func (r *Registry) WatchService(ctx context.Context, serviceName string) (chan []ServiceInstance, error) {
	serviceKey := path.Join(r.opts.RootPath, serviceName)

	watchCh := make(chan []ServiceInstance, 10)

	services, err := r.GetService(ctx, serviceName)
	if err != nil {
		return nil, fmt.Errorf("获取服务实例失败: %w", err)
	}
	// 发送初始服务实例
	watchCh <- services
	// 监视服务变更
	etcdWatchCh := r.client.Watch(ctx, serviceKey, clientv3.WithPrefix())
	// 处理服务变更
	go func() {
		defer close(watchCh)
		for {
			select {
			case <-ctx.Done():
				return
			case watchResp := <-etcdWatchCh:
				if watchResp.Canceled {
					return
				}

				// 有服务变更，获取最新服务实例
				updatedServices, err := r.GetService(ctx, serviceName)
				if err != nil {
					r.opts.Logger.Printf("获取服务实例失败: %v", err)
					continue
				}
				watchCh <- updatedServices
			}
		}
	}()

	// 返回服务实例变更通道
	return watchCh, nil

}

// Close 关闭注册中心
func (r *Registry) Close() error {
	r.cancel()

	if err := r.client.Close(); err != nil {
		return fmt.Errorf("关闭etcd客户端失败: %w", err)
	}

	return nil
}
