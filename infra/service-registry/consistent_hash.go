package registry

import (
	"errors"
	"hash/crc32"
	"sort"
	"strconv"
	"sync"
)

// 一致性哈希相关常量
const (
	// DefaultVirtualReplicas 默认的虚拟节点数量
	DefaultVirtualReplicas = 10
)

// ConsistentHashBalancer 一致性哈希负载均衡器
type ConsistentHashBalancer struct {
	mu           sync.RWMutex               // 读写锁
	hashRing     map[uint32]string          // 哈希环，key为哈希值，value为服务实例ID
	sortedKeys   []uint32                   // 排序后的哈希值
	virtualNodes int                        // 虚拟节点数量
	serviceMap   map[string]ServiceInstance // 服务实例映射，key为服务实例ID，value为服务实例
}

// NewConsistentHashBalancer 创建一个新的一致性哈希负载均衡器
func NewConsistentHashBalancer(virtualNodes int) *ConsistentHashBalancer {
	if virtualNodes <= 0 {
		virtualNodes = DefaultVirtualReplicas
	}
	return &ConsistentHashBalancer{
		hashRing:     make(map[uint32]string),
		sortedKeys:   make([]uint32, 0),
		virtualNodes: virtualNodes,
		serviceMap:   make(map[string]ServiceInstance),
	}
}

// updateRing 更新哈希环
func (b *ConsistentHashBalancer) updateRing(services []ServiceInstance) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// 清空现有数据
	b.hashRing = make(map[uint32]string)
	b.sortedKeys = make([]uint32, 0)
	b.serviceMap = make(map[string]ServiceInstance)

	// 为每个服务实例创建虚拟节点
	for _, service := range services {
		b.serviceMap[service.ID] = service

		for i := 0; i < b.virtualNodes; i++ {
			// 为虚拟节点生成哈希值
			key := b.hashKey(service.ID + "-" + strconv.Itoa(i))
			b.hashRing[key] = service.ID
			b.sortedKeys = append(b.sortedKeys, key)
		}
	}

	// 对哈希值进行排序
	sort.Slice(b.sortedKeys, func(i, j int) bool {
		return b.sortedKeys[i] < b.sortedKeys[j]
	})
}

// hashKey 计算哈希值
func (b *ConsistentHashBalancer) hashKey(key string) uint32 {
	return crc32.ChecksumIEEE([]byte(key))
}

// Select 根据一致性哈希算法选择服务实例
func (b *ConsistentHashBalancer) Select(services []ServiceInstance, key string) (ServiceInstance, error) {
	if len(services) == 0 {
		return ServiceInstance{}, ErrNoAvailableService
	}

	// 如果只有一个实例，直接返回
	if len(services) == 1 {
		return services[0], nil
	}

	// 更新哈希环
	b.updateRing(services)

	// 计算key的哈希值
	hash := b.hashKey(key)

	// 在哈希环上查找
	b.mu.RLock()
	defer b.mu.RUnlock()

	// 哈希环为空
	if len(b.sortedKeys) == 0 {
		return ServiceInstance{}, errors.New("哈希环为空")
	}

	// 找到第一个大于等于hash的节点
	idx := sort.Search(len(b.sortedKeys), func(i int) bool {
		return b.sortedKeys[i] >= hash
	})

	// 如果没有找到，使用环的第一个节点
	if idx == len(b.sortedKeys) {
		idx = 0
	}

	// 获取服务实例ID
	serviceID := b.hashRing[b.sortedKeys[idx]]
	service, ok := b.serviceMap[serviceID]
	if !ok {
		return ServiceInstance{}, errors.New("服务实例未找到")
	}

	return service, nil
}

// Type 返回负载均衡器类型
func (b *ConsistentHashBalancer) Type() BalancerType {
	return BalancerConsistentHash
}
