package relay

// 渠道亲和路由：把同一对话的请求绑到同一渠道，使上游内部缓存
// （Anthropic prompt caching / OpenAI prompt cache）有机会命中。
//
// 亲和键优先取请求体的会话级标识（Anthropic 的 metadata.user_id / OpenAI 的
// prompt_cache_key），退化到 API Key ID；无键时不启用亲和，走现有加权随机。
// 状态存储为进程内 map + TTL（单实例前提）。多实例扩展点见 affinityTable 注释。
//
// 亲和层包裹 SelectChannel，不替换它：命中亲和键且绑定渠道在顶层候选层 → 绕过
// 加权随机直接返回绑定渠道；绑定失效（不在候选集/被排除/属较低优先级层）→ 回退
// SelectChannel 加权随机；调用成功后重绑（更新绑定 + TTL 续期）。
//
// provider 过滤已在外层（filterByProvider）完成，本层只在候选切片内选择，
// 完全感知不到 provider。

import (
	"strconv"
	"sync"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// affinityTTL 亲和绑定默认有效期。覆盖 Anthropic extended cache（1 小时）的一半，
// 命中即续期（bind 在每次调用成功后执行）。方案 C.3 建议可配置为系统设置项
// affinity_ttl_sec；当前以常量实现，待设置项落地时替换为读取。
const affinityTTL = 30 * time.Minute

// affinityMaxEntries 亲和表容量上限。超过后先清过期项，仍超则按最久未访问淘汰，
// 防内存膨胀。方案 C.3 建议可配置。
const affinityMaxEntries = 10000

// affinitySource 亲和键来源标记，用于日志与指标归因。
type affinitySource string

const (
	affinitySourceNone     affinitySource = "none"      // 无法提取，不启用亲和
	affinitySourceUserID   affinitySource = "user_id"   // Anthropic metadata.user_id
	affinitySourceCacheKey affinitySource = "cache_key" // OpenAI prompt_cache_key
	affinitySourceAPIKey   affinitySource = "api_key"   // 退化到 API Key ID
)

// affinityOutcome 一次亲和选择的结果分类，驱动命中/未命中/漂移指标计数。
type affinityOutcome int

const (
	affinityOff   affinityOutcome = iota // 无亲和键或无亲和表 → 纯加权随机
	affinityMiss                         // 有亲和键但无既有绑定（首次请求）
	affinityHit                          // 既有绑定有效，命中绑定渠道
	affinityDrift                        // 既有绑定失效（渠道不在顶层/被排除）→ 回退加权随机
)

// affinityTable 渠道亲和绑定表（并发安全，进程内存态）。
//
// 键空间：model + ":" + affinityKey。不同模型的候选集不同，绑定各自独立。
// 绑定带 TTL（命中即续期），过期后下次请求重新选择。容量超上限时按最久未访问淘汰。
//
// 多实例扩展点（当前不实现）：
// 多实例部署下进程内 map 不共享，每实例各自绑定，命中率按实例数比例下降。
// 正确解法是把本结构换成共享存储（如 Redis）实现：lookup 对应 GET（带 PXAT 过期）、
// bind 对应 SET EX、容量上限由 Redis maxmemory policy 接管。公开方法签名保持不变，
// 调用方（selectWithAffinity / relayWithRetry）无需改动。
// 单实例前提由决策 2026-08-07 确认；扩多实例时引入 Redis backend 即可。
type affinityTable struct {
	mu      sync.Mutex
	entries map[string]affinityEntry
	// now 可注入的时钟（测试用）；nil 时用 time.Now。
	now func() time.Time
}

type affinityEntry struct {
	channelID  int64
	expiresAt  time.Time
	lastAccess time.Time
}

func newAffinityTable() *affinityTable {
	return &affinityTable{now: time.Now}
}

func (t *affinityTable) nowOrDefault() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

// lookup 查询绑定。命中且未过期时返回渠道 ID；过期或未绑定返回 (0, false)。
// 命中时刷新 lastAccess（用于容量淘汰的近似 LRU）。key 为空时直接返回未命中。
func (t *affinityTable) lookup(model, key string) (int64, bool) {
	if key == "" {
		return 0, false
	}
	k := model + ":" + key
	now := t.nowOrDefault()
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[k]
	if !ok || now.After(e.expiresAt) {
		return 0, false
	}
	e.lastAccess = now
	t.entries[k] = e
	return e.channelID, true
}

// bind 记录绑定并续期 TTL。key 为空时无操作。容量超上限时触发淘汰（见 evictLocked）。
func (t *affinityTable) bind(model, key string, channelID int64) {
	if key == "" {
		return
	}
	k := model + ":" + key
	now := t.nowOrDefault()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.entries == nil {
		t.entries = make(map[string]affinityEntry)
	}
	t.entries[k] = affinityEntry{
		channelID:  channelID,
		expiresAt:  now.Add(affinityTTL),
		lastAccess: now,
	}
	if len(t.entries) > affinityMaxEntries {
		t.evictLocked(now)
	}
}

// evictLocked 容量淘汰：先清过期项，仍超上限则按 lastAccess 升序淘汰至容量内。
// 调用方持锁。O(n) 扫描仅在溢出时触发，容量场景下可接受。
func (t *affinityTable) evictLocked(now time.Time) {
	for k, e := range t.entries {
		if now.After(e.expiresAt) {
			delete(t.entries, k)
		}
	}
	for len(t.entries) > affinityMaxEntries {
		var oldestKey string
		var oldestAt time.Time
		first := true
		for k, e := range t.entries {
			if first || e.lastAccess.Before(oldestAt) {
				oldestKey = k
				oldestAt = e.lastAccess
				first = false
			}
		}
		delete(t.entries, oldestKey)
	}
}

// extractAffinityKey 按方案 C 节优先级提取亲和键与来源。
//
// 优先级（命中即停）：
//  1. Anthropic 下游：请求体 metadata.user_id（会话级，Claude Code 按会话设置）
//  2. OpenAI 下游：请求体 prompt_cache_key（Codex/对话场景的会话级缓存键）
//  3. 退化：API Key ID（用户级，粗于会话但稳定；牺牲负载均衡换缓存命中）
//  4. 无键：返回 ("", none)，不启用亲和
//
// 退化键为何不用「用户+模型」（过粗）：同一用户的无关对话会全部聚到一个渠道，
// 既不提升缓存命中又破坏负载均衡。会话级标识才是缓存命中的正确粒度——网关侧
// 只需保证「同对话同渠道（同上游账号）」，上游内部缓存即可基于内容前缀命中。
func extractAffinityKey(body map[string]any, ds downstream, keyID int64) (string, affinitySource) {
	// 1. 会话级键
	if ds == dsAnthropic {
		if md, ok := body["metadata"].(map[string]any); ok {
			if uid, ok := md["user_id"].(string); ok && uid != "" {
				return uid, affinitySourceUserID
			}
		}
	}
	if ds == dsOpenAI {
		if pck, ok := body["prompt_cache_key"].(string); ok && pck != "" {
			return pck, affinitySourceCacheKey
		}
	}
	// 2. 退化到 API Key ID
	if keyID > 0 {
		return "key:" + strconv.FormatInt(keyID, 10), affinitySourceAPIKey
	}
	// 3. 无键
	return "", affinitySourceNone
}

// selectWithAffinity 在 filterByProvider 产出的候选切片内做亲和选择。
//
// 「优先级 > 亲和」：候选集按 priority 降序排列（ListEnabledForModel 保证），
// 亲和仅在顶层候选层（最高优先级、未排除）内生效。绑定渠道属顶层 → 命中绕过加权随机；
// 绑定渠道属较低优先级层或不在候选集/被排除 → 失效回退（drift）走 SelectChannel。
//
// 无亲和键（key 为空）或无亲和表 → 直接 SelectChannel，行为与未启用亲和完全一致。
// 返回选中的渠道与亲和结果分类（off/miss/hit/drift），调用方据此计数指标、记日志、
// 并在成功后调 bind 重绑。
func selectWithAffinity(channels []store.Channel, exclude map[int64]bool,
	table *affinityTable, model, affinityKey string) (*store.Channel, affinityOutcome) {

	if affinityKey == "" || table == nil {
		return SelectChannel(channels, exclude), affinityOff
	}
	boundID, ok := table.lookup(model, affinityKey)
	if !ok {
		return SelectChannel(channels, exclude), affinityMiss
	}
	// 确定顶层优先级（第一个未排除渠道的 priority，依赖候选集已按 priority DESC 排序）。
	topPrio, hasTop := topAvailablePriority(channels, exclude)
	if !hasTop {
		return SelectChannel(channels, exclude), affinityDrift
	}
	for i := range channels {
		if channels[i].ID == boundID && channels[i].Priority == topPrio && !exclude[boundID] {
			return &channels[i], affinityHit
		}
	}
	// 绑定渠道不在顶层候选层（属较低优先级层、被排除或不在候选集）→ 漂移回退
	return SelectChannel(channels, exclude), affinityDrift
}

// topAvailablePriority 返回候选集中最高优先级（未排除渠道中的最大 priority）。
// 候选集按 priority DESC 排列，故第一个未排除渠道即为顶层。无可用候选返回 (0, false)。
func topAvailablePriority(channels []store.Channel, exclude map[int64]bool) (int, bool) {
	for i := range channels {
		if !exclude[channels[i].ID] {
			return channels[i].Priority, true
		}
	}
	return 0, false
}
