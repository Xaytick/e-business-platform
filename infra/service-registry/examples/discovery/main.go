package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var (
	serviceName    string
	registryURL    string
	balancerType   string
	requestCount   int
	intervalMillis int
	logger         *log.Logger
)

func init() {
	flag.StringVar(&serviceName, "service", "demo-service", "要发现的服务名称")
	flag.StringVar(&registryURL, "registry", "http://localhost:8500", "注册中心地址")
	flag.StringVar(&balancerType, "balancer", "random", "负载均衡策略: random, round_robin, consistent_hash")
	flag.IntVar(&requestCount, "count", 10, "请求次数")
	flag.IntVar(&intervalMillis, "interval", 1000, "请求间隔(毫秒)")
}

func main() {
	flag.Parse()

	// 初始化日志
	logger = log.New(os.Stdout, "[服务发现] ", log.LstdFlags)

	// 创建注册中心客户端
	client := registry.NewClient(registry.ClientOptions{
		RegistryEndpoint: registryURL,
		HTTPTimeout:      5 * time.Second,
	})

	// 选择负载均衡器
	var balancer registry.Balancer
	switch balancerType {
	case "round_robin":
		balancer = registry.NewRoundRobinBalancer()
		logger.Printf("使用轮询负载均衡器")
	case "consistent_hash":
		balancer = registry.NewConsistentHashBalancer(10)
		logger.Printf("使用一致性哈希负载均衡器")
	default:
		balancer = &registry.RandomBalancer{}
		logger.Printf("使用随机负载均衡器")
	}

	// 创建服务发现对象
	discovery := registry.NewDiscovery(registry.DiscoveryOptions{
		Registry: nil, // 不使用直接的注册中心
		Balancer: balancer,
		Logger:   logger,
	})

	// 捕获中断信号
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// 创建上下文
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 启动请求协程
	go func() {
		for i := 0; i < requestCount; i++ {
			select {
			case <-ctx.Done():
				return
			default:
				// 获取服务实例
				services, err := client.GetService(ctx, serviceName)
				if err != nil {
					logger.Printf("获取服务失败: %v", err)
					time.Sleep(time.Duration(intervalMillis) * time.Millisecond)
					continue
				}

				if len(services) == 0 {
					logger.Printf("没有可用的服务实例")
					time.Sleep(time.Duration(intervalMillis) * time.Millisecond)
					continue
				}

				// 使用负载均衡器选择服务实例
				instance, err := balancer.Select(services, fmt.Sprintf("request-%d", i))
				if err != nil {
					logger.Printf("选择服务实例失败: %v", err)
					time.Sleep(time.Duration(intervalMillis) * time.Millisecond)
					continue
				}

				// 调用服务
				if err := callService(instance); err != nil {
					logger.Printf("调用服务失败: %v", err)
				}

				time.Sleep(time.Duration(intervalMillis) * time.Millisecond)
			}
		}
		logger.Printf("所有请求已完成")
		// 通知主协程退出
		stop <- os.Interrupt
	}()

	<-stop
	logger.Println("程序已退出")
}

// callService 调用服务实例
func callService(instance registry.ServiceInstance) error {
	if len(instance.Endpoints) == 0 {
		return fmt.Errorf("服务实例没有端点")
	}

	// 构建请求URL
	url := instance.Endpoints[0] + "/hello"

	// 发送HTTP请求
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("发送请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应体
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("读取响应失败: %w", err)
	}

	logger.Printf("请求 [%s] 响应状态码: %d, 内容: %s", instance.ID, resp.StatusCode, string(body))
	return nil
}
