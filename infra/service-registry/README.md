# 服务注册与发现中心

基于etcd的服务注册与发现组件，为微服务架构提供服务注册、发现和负载均衡功能。

## 主要功能

1. **服务注册**：微服务可以将自己注册到注册中心，包括服务名称、版本、端点等信息
2. **服务发现**：应用可以从注册中心发现可用的服务实例
3. **负载均衡**：支持多种负载均衡策略（随机、轮询、一致性哈希）
4. **健康检查**：定期检查服务实例的健康状态
5. **服务监控**：监控服务实例的状态变化

## 组件结构

```
service-registry/
├── cmd/
│   └── server/        # 注册中心服务器
├── examples/
│   ├── demo/          # 服务注册示例
│   └── discovery/     # 服务发现示例
├── registry.go        # 服务注册核心
├── discovery.go       # 服务发现核心
├── client.go          # 客户端API
├── health.go          # 健康检查
└── consistent_hash.go # 一致性哈希负载均衡
```

## 快速开始

### 前提条件

- Go 1.16+
- etcd 3.4+

### 安装etcd

```bash
# 使用Docker安装
docker run -d -p 2379:2379 -p 2380:2380 --name etcd \
  --env ALLOW_NONE_AUTHENTICATION=yes \
  bitnami/etcd:latest
```

### 启动注册中心服务器

```bash
go run infra/service-registry/cmd/server/main.go --port=8500
```

### 运行示例服务

```bash
# 启动第一个服务实例
go run infra/service-registry/examples/demo/main.go --port=8081 --name=demo-service --version=v1.0.0

# 启动第二个服务实例
go run infra/service-registry/examples/demo/main.go --port=8082 --name=demo-service --version=v1.0.0
```

### 使用服务发现

```bash
# 使用随机负载均衡
go run infra/service-registry/examples/discovery/main.go --service=demo-service --balancer=random --count=10

# 使用轮询负载均衡
go run infra/service-registry/examples/discovery/main.go --service=demo-service --balancer=round_robin --count=10

# 使用一致性哈希负载均衡
go run infra/service-registry/examples/discovery/main.go --service=demo-service --balancer=consistent_hash --count=10
```

## 如何在应用中使用

### 服务注册

```go
package main

import (
	"context"
	"log"
	
	"e-business-platform/infra/service-registry"
)

func main() {
	// 创建服务实例
	serviceInstance := registry.ServiceInstance{
		ID:      "service-1",
		Name:    "my-service",
		Version: "v1.0.0",
		Endpoints: []string{
			"http://localhost:8080",
		},
		Metadata: map[string]string{
			"version": "v1.0.0",
		},
		Status: "UP",
	}
	
	// 创建客户端
	client := registry.NewClient(registry.DefaultClientOptions())
	
	// 注册服务
	if err := client.Register(context.Background(), serviceInstance); err != nil {
		log.Fatalf("注册服务失败: %v", err)
	}
	
	// 在程序结束时注销服务
	defer client.Close()
	
	// 应用逻辑...
}
```

### 服务发现与负载均衡

```go
package main

import (
	"context"
	"log"
	
	"e-business-platform/infra/service-registry"
)

func main() {
	// 创建注册中心客户端
	client := registry.NewClient(registry.DefaultClientOptions())
	
	// 获取服务实例
	services, err := client.GetService(context.Background(), "my-service")
	if err != nil {
		log.Fatalf("获取服务失败: %v", err)
	}
	
	// 创建负载均衡器
	balancer := registry.NewRoundRobinBalancer()
	
	// 选择服务实例
	instance, err := balancer.Select(services, "request-key")
	if err != nil {
		log.Fatalf("选择服务实例失败: %v", err)
	}
	
	// 使用服务实例
	log.Printf("选择的服务实例: %s, 端点: %v", instance.ID, instance.Endpoints)
	
	// 调用服务...
}
```

## API文档

### 注册中心HTTP API

- `GET /v1/registry/services` - 获取所有服务
- `GET /v1/registry/services/{name}` - 获取指定服务的实例列表
- `POST /v1/registry/services` - 注册服务实例
- `DELETE /v1/registry/services/{name}/{id}` - 注销服务实例
- `GET /v1/registry/health/{id}` - 获取服务实例的健康状态

## 配置选项

### 注册中心服务器

```json
{
  "port": 8500,
  "etcd_endpoints": ["localhost:2379"],
  "etcd_dial_timeout": 5000000000,
  "registry_ttl": 30000000000,
  "root_path": "/microservices/registry",
  "health_check_interval": 30000000000
}
```

### 客户端

```go
opts := registry.ClientOptions{
	RegistryEndpoint: "http://localhost:8500",
	HTTPTimeout:      5 * time.Second,
	RegisterTTL:      30 * time.Second,
}
```

## 高级功能

### 自定义负载均衡器

实现`Balancer`接口：

```go
type MyBalancer struct{}

func (b *MyBalancer) Select(services []registry.ServiceInstance, key string) (registry.ServiceInstance, error) {
	// 自定义选择逻辑
	return services[0], nil
}

func (b *MyBalancer) Type() registry.BalancerType {
	return "my_balancer"
}
```

### 自定义健康检查

实现`HealthCheck`接口：

```go
type MyHealthCheck struct{}

func (h *MyHealthCheck) Check(ctx context.Context, instance registry.ServiceInstance) (registry.HealthStatus, error) {
	// 自定义检查逻辑
	return registry.HealthStatusUp, nil
}

func (h *MyHealthCheck) Type() string {
	return "my_health_check"
}
```

## 贡献指南

欢迎贡献代码、提交问题和改进建议！ 