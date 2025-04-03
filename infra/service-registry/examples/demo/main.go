package main

import (
	"context"
	registry "e-business-platform/infra/service-registry"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

var (
	servicePort    int
	serviceName    string
	serviceVersion string
	registryURL    string
	logger         *log.Logger
)

func init() {
	flag.IntVar(&servicePort, "port", 8080, "服务端口")
	flag.StringVar(&serviceName, "name", "demo-service", "服务名称")
	flag.StringVar(&serviceVersion, "version", "v1.0.0", "服务版本")
	flag.StringVar(&registryURL, "registry", "http://localhost:8500", "注册中心地址")
}

func main() {
	flag.Parse()

	// 创建实例ID
	instanceID := fmt.Sprintf("%s-%s", serviceName, uuid.New().String()[:8])

	// 初始化日志
	logger = log.New(os.Stdout, fmt.Sprintf("[%s] ", instanceID), log.LstdFlags)
	logger.Printf("启动服务: %s (版本: %s)", serviceName, serviceVersion)

	// 创建HTTP路由
	r := mux.NewRouter()
	r.HandleFunc("/hello", helloHandler).Methods("GET")
	r.HandleFunc("/health", healthHandler).Methods("GET")

	// 创建HTTP服务器
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", servicePort),
		Handler: r,
	}

	// 创建服务实例
	serviceInstance := registry.ServiceInstance{
		ID:      instanceID,
		Name:    serviceName,
		Version: serviceVersion,
		Endpoints: []string{
			fmt.Sprintf("http://localhost:%d", servicePort),
		},
		Metadata: map[string]string{
			"version": serviceVersion,
		},
		Status: "UP",
	}

	// 创建注册中心客户端
	client := registry.NewClient(registry.ClientOptions{
		RegistryEndpoint: registryURL,
		HTTPTimeout:      5 * time.Second,
		RegisterTTL:      30 * time.Second,
	})

	// 注册服务
	logger.Printf("正在向注册中心注册服务: %s", registryURL)
	if err := client.Register(context.Background(), serviceInstance); err != nil {
		logger.Fatalf("注册服务失败: %v", err)
	}
	logger.Printf("服务注册成功")

	// 启动HTTP服务器
	go func() {
		logger.Printf("HTTP服务器已启动，监听端口: %d", servicePort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatalf("HTTP服务器启动失败: %v", err)
		}
	}()

	// 等待中断信号
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	logger.Println("正在关闭服务...")

	// 优雅关闭
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 注销服务
	if err := client.Deregister(ctx); err != nil {
		logger.Printf("注销服务失败: %v", err)
	} else {
		logger.Printf("服务注销成功")
	}

	// 关闭HTTP服务器
	if err := server.Shutdown(ctx); err != nil {
		logger.Printf("HTTP服务器关闭失败: %v", err)
	}

	logger.Println("服务已关闭")
}

func helloHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "Hello from %s (version: %s)\n", serviceName, serviceVersion)
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}
