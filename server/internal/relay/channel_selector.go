package relay

// ChannelSelector 封装渠道选择：在 provider 过滤后的候选切片内做加权随机 +
// 渠道亲和（绑定查询/重绑）。把原本散落在 Engine 的亲和状态（affinityTable +
// sync.Once 懒初始化）与选择调用收敛到一处，Engine 不再直接持有亲和状态
// （C2 设计 step 3：Engine 减 affinityOnce/affinityTab 2 字段）。
//
// 选择纯逻辑（SelectChannel / selectWithAffinity / extractAffinityKey）保持为
// 包级纯函数，便于无 DB 单测；本结构只持有可变状态（亲和表）并提供选择入口。
// 多实例扩展点见 affinityTable 注释（进程内态，单实例前提）。

import (
	"sync"

	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// ChannelSelector 渠道选择器：持有亲和绑定表，提供选择与重绑入口。
type ChannelSelector struct {
	// once 保证亲和表只懒初始化一次（并发安全）。
	once sync.Once
	// affinity 渠道亲和绑定表（内存态，TTL 续期）。
	// 测试可预置自定义表（如注入可控时钟）；nil 时首次 Select 会新建默认表。
	affinity *affinityTable
}

// NewChannelSelector 创建渠道选择器；亲和表懒初始化（首次 Select 时创建）。
func NewChannelSelector() *ChannelSelector {
	return &ChannelSelector{}
}

// table 返回亲和绑定表（懒初始化，并发安全）。
// 测试可预置 c.affinity 注入可控时钟，本方法尊重已设置的实例（Once 内做 nil 检查）。
func (c *ChannelSelector) table() *affinityTable {
	c.once.Do(func() {
		if c.affinity == nil {
			c.affinity = newAffinityTable()
		}
	})
	return c.affinity
}

// Select 在候选切片内做一次渠道选择。
// 返回选中的渠道与亲和结果分类（off/miss/hit/drift），调用方据此计数指标、
// 记日志、并在成功后调 Bind 重绑。tried 由调用方维护（本请求已选/已失败的渠道 ID）。
func (c *ChannelSelector) Select(channels []store.Channel, tried map[int64]bool,
	model, affinityKey string) (*store.Channel, affinityOutcome) {
	return selectWithAffinity(channels, tried, c.table(), model, affinityKey)
}

// Bind 把亲和键绑到成功渠道并续期 TTL。key 为空时无操作。
// 漂移场景下重绑到新渠道、首次场景（miss）建立绑定均经此入口。
func (c *ChannelSelector) Bind(model, key string, channelID int64) {
	c.table().bind(model, key, channelID)
}
