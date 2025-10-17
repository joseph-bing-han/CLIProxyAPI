package util

import (
	"strings"

	tiktoken "github.com/pkoukk/tiktoken-go"
)

// EstimateTokensForModel 使用 tiktoken-go 基于模型名近似估算请求字节的 token 数
// 注意: 不同提供方/模型的计数规则可能略有差异, 该方法提供通用近似, 可按需扩展映射
func EstimateTokensForModel(model string, content []byte) int {
	if len(content) == 0 {
		return 0
	}
	name := strings.TrimSpace(model)
	enc, err := tiktoken.EncodingForModel(name)
	if err != nil {
		// 未知模型时回退到 cl100k_base, 适配 GPT-4/3.5/5 等通用编码
		enc, err = tiktoken.GetEncoding("cl100k_base")
		if err != nil {
			// 极端情况下回退字符近似
			n := len(content) / 4
			if n <= 0 {
				n = 1
			}
			return n
		}
	}
	tokens := enc.Encode(string(content), nil, nil)
	if l := len(tokens); l > 0 {
		return l
	}
	return 0
}
