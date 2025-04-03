package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// ServerConfig 服务器配置
type ServerConfig struct {
	Port                int           `json:"port"`
	EtcdEndpoints       []string      `json:"etcd_endpoints"`
	EtcdDialTimeout     time.Duration `json:"etcd_dial_timeout"`
	RegistryTTL         time.Duration `json:"registry_ttl"`
	RootPath            string        `json:"root_path"`
	HealthCheckInterval time.Duration `json:"health_check_interval"`
}

// DefaultConfig 返回默认配置
func DefaultConfig() ServerConfig {
	return ServerConfig{
		Port:                8500,
		EtcdEndpoints:       []string{"localhost:2379"},
		EtcdDialTimeout:     5 * time.Second,
		RegistryTTL:         30 * time.Second,
		RootPath:            registry.DefaultRootPath,
		HealthCheckInterval: 30 * time.Second,
	}
}

// 全局变量
var (
	cfg           ServerConfig
	reg           *registry.Registry
	healthChecker *registry.HealthChecker
	logger        *log.Logger
)

func main() {
	// 解析命令行参数
	configFile := flag.String("config", "", "配置文件路径")
	port := flag.Int("port", 0, "服务端口")
	flag.Parse()

	// 初始化日志
	logger = log.New(os.Stdout, "[服务注册中心] ", log.LstdFlags)

	// 加载配置
	cfg = DefaultConfig()
	if *configFile != "" {
		if err := loadConfig(*configFile, &cfg); err != nil {
			logger.Fatalf("加载配置失败: %v", err)
		}
	}

	// 覆盖端口（如果提供）
	if *port > 0 {
		cfg.Port = *port
	}

	// 初始化服务注册中心
	var err error
	reg, err = registry.NewRegistry(registry.Options{
		EtcdConfig: clientv3.Config{
			Endpoints:   cfg.EtcdEndpoints,
			DialTimeout: cfg.EtcdDialTimeout,
		},
		TTL:      cfg.RegistryTTL,
		RootPath: cfg.RootPath,
		Logger:   logger,
	})
	if err != nil {
		logger.Fatalf("创建服务注册中心失败: %v", err)
	}

	// 初始化健康检查器
	httpHealthCheck := registry.NewHTTPHealthCheck("/health", 5*time.Second)
	tcpHealthCheck := registry.NewTCPHealthCheck(5 * time.Second)

	healthChecker = registry.NewHealthChecker(registry.HealthCheckerOptions{
		Registry: reg,
		Interval: cfg.HealthCheckInterval,
		Checkers: []registry.HealthCheck{httpHealthCheck, tcpHealthCheck},
		Logger:   logger,
	})

	// 启动健康检查
	healthChecker.Start()

	// 创建路由
	r := mux.NewRouter()

	// 注册API路由
	r.HandleFunc("/v1/registry/services", listServicesHandler).Methods("GET")
	r.HandleFunc("/v1/registry/services/{name}", getServiceHandler).Methods("GET")
	r.HandleFunc("/v1/registry/services", registerServiceHandler).Methods("POST")
	r.HandleFunc("/v1/registry/services/{name}/{id}", deregisterServiceHandler).Methods("DELETE")
	r.HandleFunc("/v1/registry/health/{id}", getHealthStatusHandler).Methods("GET")

	// 添加健康检查路由
	r.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// 创建HTTP服务器
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: r,
	}

	// 启动服务器
	go func() {
		logger.Printf("服务注册中心已启动，监听端口: %d", cfg.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatalf("服务器启动失败: %v", err)
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

	// 停止健康检查
	healthChecker.Stop()

	// 关闭HTTP服务器
	if err := server.Shutdown(ctx); err != nil {
		logger.Printf("服务器关闭失败: %v", err)
	}

	// 关闭注册中心
	if err := reg.Close(); err != nil {
		logger.Printf("注册中心关闭失败: %v", err)
	}

	logger.Println("服务已关闭")
}

// loadConfig 从文件加载配置
func loadConfig(filename string, config *ServerConfig) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	return decoder.Decode(config)
}

// listServicesHandler 获取所有服务
func listServicesHandler(w http.ResponseWriter, r *http.Request) {
	// 获取服务列表逻辑
	// 由于当前registry的设计，这里需要额外实现
	// 这里仅返回一个空数组作为示例
	services := []string{}

	respondJSON(w, http.StatusOK, services)
}

// getServiceHandler 获取指定服务的实例列表
func getServiceHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serviceName := vars["name"]

	if serviceName == "" {
		respondError(w, http.StatusBadRequest, "服务名称不能为空")
		return
	}

	// 获取服务实例
	services, err := reg.GetService(r.Context(), serviceName)
	if err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("获取服务失败: %v", err))
		return
	}

	respondJSON(w, http.StatusOK, services)
}

// registerServiceHandler 注册服务
func registerServiceHandler(w http.ResponseWriter, r *http.Request) {
	var service registry.ServiceInstance

	// 解析请求体
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&service); err != nil {
		respondError(w, http.StatusBadRequest, fmt.Sprintf("解析请求失败: %v", err))
		return
	}

	// 注册服务
	if err := reg.Register(r.Context(), service); err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("注册服务失败: %v", err))
		return
	}

	respondJSON(w, http.StatusCreated, service)
}

// deregisterServiceHandler 注销服务
func deregisterServiceHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serviceName := vars["name"]
	serviceID := vars["id"]

	if serviceName == "" || serviceID == "" {
		respondError(w, http.StatusBadRequest, "服务名称和ID不能为空")
		return
	}

	// 创建服务实例
	service := registry.ServiceInstance{
		ID:   serviceID,
		Name: serviceName,
	}

	// 注销服务
	if err := reg.Deregister(r.Context(), service); err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("注销服务失败: %v", err))
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"message": "服务注销成功"})
}

// getHealthStatusHandler 获取服务健康状态
func getHealthStatusHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serviceID := vars["id"]

	if serviceID == "" {
		respondError(w, http.StatusBadRequest, "服务ID不能为空")
		return
	}

	// 获取健康状态
	status := healthChecker.GetStatus(serviceID)

	respondJSON(w, http.StatusOK, map[string]string{
		"id":     serviceID,
		"status": string(status),
	})
}

// respondJSON 返回JSON响应
func respondJSON(w http.ResponseWriter, status int, payload interface{}) {
	response, err := json.Marshal(payload)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(response)
}

// respondError 返回错误响应
func respondError(w http.ResponseWriter, code int, message string) {
	respondJSON(w, code, map[string]string{"error": message})
}
