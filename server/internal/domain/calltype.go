package domain

// CallType 调用类型：面向运营报表的派生维度，不是 usage_logs 的物理列。
// 由模型形态（Modality）与是否流式组合得出，权威定义见 docs/glossary.md 的 CallType 节。
type CallType string

const (
	// CallTypeEmbedding 向量嵌入模型调用。
	CallTypeEmbedding CallType = "embedding"
	// CallTypeImage 图像生成模型调用。
	CallTypeImage CallType = "image"
	// CallTypeStream 文本对话模型的流式调用。
	CallTypeStream CallType = "stream"
	// CallTypeNonStream 文本对话模型的非流式调用。
	CallTypeNonStream CallType = "non_stream"
	// CallTypeOther 模型已删除或不在目录中时（modality 为 NULL）的兜底桶。
	CallTypeOther CallType = "other"
)
