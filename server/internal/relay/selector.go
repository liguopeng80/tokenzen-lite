package relay

import (
	"math/rand"

	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// SelectChannel 从候选渠道中选择一个：priority 降序分层，层内按 weight+1
// 加权随机；exclude 中的渠道（本请求已失败）跳过。无可用渠道返回 nil。
// 传入的 channels 必须已按 priority 降序排列（ListEnabledForModel 保证）。
func SelectChannel(channels []store.Channel, exclude map[int64]bool) *store.Channel {
	i := 0
	for i < len(channels) {
		// 取出当前最高优先级层
		tierPriority := channels[i].Priority
		var tier []*store.Channel
		for i < len(channels) && channels[i].Priority == tierPriority {
			if !exclude[channels[i].ID] {
				tier = append(tier, &channels[i])
			}
			i++
		}
		if len(tier) == 0 {
			continue // 本层全部被排除，降层
		}
		totalWeight := 0
		for _, c := range tier {
			totalWeight += c.Weight + 1
		}
		pick := rand.Intn(totalWeight)
		for _, c := range tier {
			pick -= c.Weight + 1
			if pick < 0 {
				return c
			}
		}
	}
	return nil
}
