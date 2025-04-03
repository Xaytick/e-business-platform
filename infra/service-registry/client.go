package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Client 注册中心客户端
type Client struct {
	httpClient       *http.Client
	registryEndpoint string
	localInstance    ServiceInstance
	registerTTL      time.Duration
	heartbeatTicker  *time.Ticker
	closeCh          chan struct{}
	isRegistered     bool
}

// ClientOptions 客户端选项
type ClientOptions struct {
	RegistryEndpoint string
	HTTPTimeout      time.Duration
	RegisterTTL      time.Duration
}

// DefaultClientOptions 返回默认客户端选项
func DefaultClientOptions() ClientOptions {
	return ClientOptions{
		RegistryEndpoint: "http://localhost:8500",
		HTTPTimeout:      5 * time.Second,
		RegisterTTL:      30 * time.Second,
	}
}

// NewClient 创建一个新的客户端
func NewClient(opts ClientOptions) *Client {
	if opts.HTTPTimeout <= 0 {
		opts.HTTPTimeout = 5 * time.Second
	}
	if opts.RegisterTTL <= 0 {
		opts.RegisterTTL = 30 * time.Second
	}

	return &Client{
		httpClient: &http.Client{
			Timeout: opts.HTTPTimeout,
		},
		registryEndpoint: opts.RegistryEndpoint,
		registerTTL:      opts.RegisterTTL,
		closeCh:          make(chan struct{}),
	}
}

// Register 注册服务实例
func (c *Client) Register(ctx context.Context, instance ServiceInstance) error {
	// 序列化服务实例
	data, err := json.Marshal(instance)
	if err != nil {
		return fmt.Errorf("序列化服务实例失败: %w", err)
	}

	// 创建请求
	url := fmt.Sprintf("%s/v1/registry/services", c.registryEndpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(data))
	if err != nil {
		return fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// 发送请求
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("发送请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 检查响应状态
	if resp.StatusCode != http.StatusCreated {
		var errResp map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
			return fmt.Errorf("注册服务失败，状态码: %d", resp.StatusCode)
		}
		return fmt.Errorf("注册服务失败: %s", errResp["error"])
	}

	// 保存本地实例
	c.localInstance = instance
	c.isRegistered = true

	// 启动心跳
	c.startHeartbeat()

	return nil
}

// Deregister 注销服务实例
func (c *Client) Deregister(ctx context.Context) error {
	if !c.isRegistered {
		return nil
	}

	// 停止心跳
	c.stopHeartbeat()

	// 创建请求
	url := fmt.Sprintf("%s/v1/registry/services/%s/%s", c.registryEndpoint, c.localInstance.Name, c.localInstance.ID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("创建请求失败: %w", err)
	}

	// 发送请求
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("发送请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 检查响应状态
	if resp.StatusCode != http.StatusOK {
		var errResp map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
			return fmt.Errorf("注销服务失败，状态码: %d", resp.StatusCode)
		}
		return fmt.Errorf("注销服务失败: %s", errResp["error"])
	}

	c.isRegistered = false

	return nil
}

// GetService 获取服务实例
func (c *Client) GetService(ctx context.Context, serviceName string) ([]ServiceInstance, error) {
	// 创建请求
	url := fmt.Sprintf("%s/v1/registry/services/%s", c.registryEndpoint, serviceName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	// 发送请求
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("发送请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 检查响应状态
	if resp.StatusCode != http.StatusOK {
		var errResp map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
			return nil, fmt.Errorf("获取服务失败，状态码: %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("获取服务失败: %s", errResp["error"])
	}

	// 解析响应
	var services []ServiceInstance
	if err := json.NewDecoder(resp.Body).Decode(&services); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	return services, nil
}

// startHeartbeat 启动心跳
func (c *Client) startHeartbeat() {
	// 停止现有心跳
	c.stopHeartbeat()

	// 创建新的心跳
	c.heartbeatTicker = time.NewTicker(c.registerTTL / 2)

	// 启动心跳协程
	go func() {
		for {
			select {
			case <-c.closeCh:
				return
			case <-c.heartbeatTicker.C:
				// 重新注册服务
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				if err := c.Register(ctx, c.localInstance); err != nil {
					// 心跳失败，可以记录日志或尝试重连
					fmt.Printf("服务心跳失败: %v\n", err)
				}
				cancel()
			}
		}
	}()
}

// stopHeartbeat 停止心跳
func (c *Client) stopHeartbeat() {
	if c.heartbeatTicker != nil {
		c.heartbeatTicker.Stop()
	}
	select {
	case c.closeCh <- struct{}{}:
	default:
	}
}

// Close 关闭客户端
func (c *Client) Close() error {
	// 注销服务
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return c.Deregister(ctx)
}
