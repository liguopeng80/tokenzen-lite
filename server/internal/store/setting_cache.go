package store

import (
	"sync"
	"time"
)

// settingsCache 是设置项的进程内 TTL 缓存（单机部署，无跨实例失效问题）。
type settingsCache struct {
	mu   sync.RWMutex
	ttl  time.Duration
	data map[string]settingsCacheItem
}

type settingsCacheItem struct {
	value    any
	expireAt time.Time
}

func newSettingsCache(ttl time.Duration) *settingsCache {
	return &settingsCache{ttl: ttl, data: make(map[string]settingsCacheItem)}
}

func (c *settingsCache) get(key string) (any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	item, ok := c.data[key]
	if !ok || time.Now().After(item.expireAt) {
		return nil, false
	}
	return item.value, true
}

func (c *settingsCache) put(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = settingsCacheItem{value: value, expireAt: time.Now().Add(c.ttl)}
}

func (c *settingsCache) invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.data, key)
}
