package registry

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

// HealthStatus 健康状态
type HealthStatus string

const (
	// HealthStatusUp 表示服务健康
	HealthStatusUp HealthStatus = "UP"
	// HealthStatusDown 表示服务不健康
	HealthStatusDown HealthStatus = "DOWN"
	// HealthStatusUnknown 表示服务健康状态未知
	HealthStatusUnknown HealthStatus = "UNKNOWN"
)

// HealthCheck 健康检查接口
type HealthCheck interface {
	// Check 执行健康检查
	Check(ctx context.Context, instance ServiceInstance) (HealthStatus, error)
	// Type 返回健康检查类型
	Type() string
}

// HTTPHealthCheck HTTP健康检查
type HTTPHealthCheck struct {
	path    string
	timeout time.Duration
	client  *http.Client
}

// NewHTTPHealthCheck 创建一个新的HTTP健康检查
func NewHTTPHealthCheck(path string, timeout time.Duration) *HTTPHealthCheck {
	if path == "" {
		path = "/health"
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &HTTPHealthCheck{
		path:    path,
		timeout: timeout,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// Check 执行HTTP健康检查
func (h *HTTPHealthCheck) Check(ctx context.Context, instance ServiceInstance) (HealthStatus, error) {
	if len(instance.Endpoints) == 0 {
		return HealthStatusUnknown, fmt.Errorf("服务实例没有端点")
	}

	// 遍历所有端点进行检查
	for _, endpoint := range instance.Endpoints {
		url := endpoint + h.path
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			continue
		}

		resp, err := h.client.Do(req)
		if err != nil {
			continue
		}
		defer resp.Body.Close()

		// 检查HTTP状态码
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return HealthStatusUp, nil
		}
	}

	return HealthStatusDown, nil
}

// Type 返回健康检查类型
func (h *HTTPHealthCheck) Type() string {
	return "HTTP"
}

// TCPHealthCheck TCP健康检查
type TCPHealthCheck struct {
	timeout time.Duration
}

// NewTCPHealthCheck 创建一个新的TCP健康检查
func NewTCPHealthCheck(timeout time.Duration) *TCPHealthCheck {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &TCPHealthCheck{
		timeout: timeout,
	}
}

// Check 执行TCP健康检查
func (t *TCPHealthCheck) Check(ctx context.Context, instance ServiceInstance) (HealthStatus, error) {
	if len(instance.Endpoints) == 0 {
		return HealthStatusUnknown, fmt.Errorf("服务实例没有端点")
	}

	// 处理endpoints，转换为host:port格式
	// 这里假设endpoints格式是http://host:port格式
	var addresses []string
	for _, endpoint := range instance.Endpoints {
		// 简单解析，实际项目中可能需要更复杂的URL解析
		// 这里只是示例
		if len(endpoint) > 7 && endpoint[:7] == "http://" {
			addresses = append(addresses, endpoint[7:])
		} else if len(endpoint) > 8 && endpoint[:8] == "https://" {
			addresses = append(addresses, endpoint[8:])
		} else {
			addresses = append(addresses, endpoint)
		}
	}

	// 遍历所有地址进行检查
	for _, address := range addresses {
		dialer := net.Dialer{Timeout: t.timeout}
		conn, err := dialer.DialContext(ctx, "tcp", address)
		if err != nil {
			continue
		}
		conn.Close()
		return HealthStatusUp, nil
	}

	return HealthStatusDown, nil
}

// Type 返回健康检查类型
func (t *TCPHealthCheck) Type() string {
	return "TCP"
}

// HealthChecker 健康检查器
type HealthChecker struct {
	registry    *Registry
	checkers    map[string]HealthCheck
	interval    time.Duration
	ctx         context.Context
	cancel      context.CancelFunc
	statusCache map[string]HealthStatus
	statusMu    sync.RWMutex
	logger      *log.Logger
}

// HealthCheckerOptions 健康检查器选项
type HealthCheckerOptions struct {
	Registry *Registry
	Interval time.Duration
	Checkers []HealthCheck
	Logger   *log.Logger
}

// NewHealthChecker 创建一个新的健康检查器
func NewHealthChecker(opts HealthCheckerOptions) *HealthChecker {
	if opts.Interval <= 0 {
		opts.Interval = 30 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = log.Default()
	}

	ctx, cancel := context.WithCancel(context.Background())

	checker := &HealthChecker{
		registry:    opts.Registry,
		checkers:    make(map[string]HealthCheck),
		interval:    opts.Interval,
		ctx:         ctx,
		cancel:      cancel,
		statusCache: make(map[string]HealthStatus),
		logger:      opts.Logger,
	}

	// 添加健康检查器
	for _, c := range opts.Checkers {
		checker.AddChecker(c)
	}

	return checker
}

// AddChecker 添加健康检查器
func (h *HealthChecker) AddChecker(checker HealthCheck) {
	h.checkers[checker.Type()] = checker
	h.logger.Printf("添加健康检查器: %s", checker.Type())
}

// RemoveChecker 移除健康检查器
func (h *HealthChecker) RemoveChecker(checkerType string) {
	delete(h.checkers, checkerType)
	h.logger.Printf("移除健康检查器: %s", checkerType)
}

// Start 启动健康检查
func (h *HealthChecker) Start() {
	go h.run()
	h.logger.Printf("健康检查器已启动，检查间隔: %v", h.interval)
}

// Stop 停止健康检查
func (h *HealthChecker) Stop() {
	h.cancel()
	h.logger.Printf("健康检查器已停止")
}

// run 执行健康检查
func (h *HealthChecker) run() {
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()

	for {
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
			h.checkAll()
		}
	}
}

// checkAll 检查所有服务实例
func (h *HealthChecker) checkAll() {
	// 获取所有服务
	// 简化实现，实际应该有服务列表获取方式
	// 这里仅作为示例，会根据实际项目调整

	// 遍历服务和实例
	// 示例遍历逻辑
	// 在实际项目中，你需要根据你的服务发现机制获取所有服务实例

	h.logger.Printf("执行健康检查...")

	// 执行健康检查
	// 实际项目中，你需要实现服务和实例的遍历逻辑
}

// GetStatus 获取服务实例的健康状态
func (h *HealthChecker) GetStatus(serviceID string) HealthStatus {
	h.statusMu.RLock()
	defer h.statusMu.RUnlock()

	status, ok := h.statusCache[serviceID]
	if !ok {
		return HealthStatusUnknown
	}

	return status
}

// SetStatus 设置服务实例的健康状态
func (h *HealthChecker) SetStatus(serviceID string, status HealthStatus) {
	h.statusMu.Lock()
	defer h.statusMu.Unlock()

	h.statusCache[serviceID] = status
}
