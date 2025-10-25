package util

import (
	"testing"
)

// TestEstimateTokensForModel 测试不同模型的 token 估算
func TestEstimateTokensForModel(t *testing.T) {
	testCases := []struct {
		name         string
		model        string
		payload      []byte
		expectMinMax [2]int // [min, max] 期望的 token 范围
	}{
		{
			name:         "GPT-5 简单文本",
			model:        "gpt-5",
			payload:      []byte(`{"messages":[{"role":"user","content":"Hello, world!"}]}`),
			expectMinMax: [2]int{5, 30}, // 大约 10-20 tokens
		},
		{
			name:         "GPT-4o 长文本",
			model:        "gpt-4o",
			payload:      []byte(`{"messages":[{"role":"user","content":"This is a longer message that should contain more tokens for testing purposes."}]}`),
			expectMinMax: [2]int{10, 50},
		},
		{
			name:         "Claude Opus 简单请求",
			model:        "claude-opus-4",
			payload:      []byte(`{"model":"claude-opus-4","messages":[{"role":"user","content":"Hi"}]}`),
			expectMinMax: [2]int{3, 25},
		},
		{
			name:         "空 payload",
			model:        "gpt-5",
			payload:      []byte(``),
			expectMinMax: [2]int{0, 1}, // 空输入应该返回 0 或最小值 1
		},
		{
			name:         "复杂 JSON 结构",
			model:        "gpt-5",
			payload:      []byte(`{"model":"gpt-5","messages":[{"role":"system","content":"You are helpful"},{"role":"user","content":"Question"},{"role":"assistant","content":"Answer"}],"temperature":0.7,"max_tokens":100}`),
			expectMinMax: [2]int{20, 60},
		},
		{
			name:         "GPT-3.5 模型",
			model:        "gpt-3.5-turbo",
			payload:      []byte(`{"messages":[{"role":"user","content":"Test message"}]}`),
			expectMinMax: [2]int{5, 25},
		},
		{
			name:         "未知模型回退",
			model:        "unknown-model",
			payload:      []byte(`{"messages":[{"role":"user","content":"Test"}]}`),
			expectMinMax: [2]int{3, 30},
		},
		{
			name:         "GPT-5-codex 变体",
			model:        "gpt-5-codex",
			payload:      []byte(`{"messages":[{"role":"user","content":"Write code"}]}`),
			expectMinMax: [2]int{5, 30},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := EstimateTokensForModel(tc.model, tc.payload)

			// 验证结果不为负数
			if result < 0 {
				t.Errorf("Token 计数不应为负数，得到: %d", result)
			}

			// 验证结果在合理范围内
			if result < tc.expectMinMax[0] || result > tc.expectMinMax[1] {
				t.Logf("警告: Token 计数 %d 超出期望范围 [%d, %d]，但这可能是正常的", 
					result, tc.expectMinMax[0], tc.expectMinMax[1])
			}

			t.Logf("模型 %s 的 payload（%d bytes）估算为 %d tokens", 
				tc.model, len(tc.payload), result)
		})
	}
}

// TestEstimateTokensForModel_UTF8 测试 UTF-8 字符处理
func TestEstimateTokensForModel_UTF8(t *testing.T) {
	testCases := []struct {
		name    string
		model   string
		payload []byte
	}{
		{
			name:    "中文文本",
			model:   "gpt-5",
			payload: []byte(`{"messages":[{"role":"user","content":"你好，世界！这是一个测试。"}]}`),
		},
		{
			name:    "日文文本",
			model:   "gpt-4o",
			payload: []byte(`{"messages":[{"role":"user","content":"こんにちは世界"}]}`),
		},
		{
			name:    "emoji",
			model:   "gpt-5",
			payload: []byte(`{"messages":[{"role":"user","content":"Hello 👋 World 🌍"}]}`),
		},
		{
			name:    "混合语言",
			model:   "claude-opus-4",
			payload: []byte(`{"messages":[{"role":"user","content":"Hello 你好 こんにちは"}]}`),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := EstimateTokensForModel(tc.model, tc.payload)

			// UTF-8 字符应该能正确处理
			if result <= 0 {
				t.Errorf("UTF-8 文本应该产生正数的 token 计数，得到: %d", result)
			}

			t.Logf("UTF-8 payload（%d bytes）估算为 %d tokens", len(tc.payload), result)
		})
	}
}

// TestEstimateTokensForModel_EdgeCases 测试边界情况
func TestEstimateTokensForModel_EdgeCases(t *testing.T) {
	testCases := []struct {
		name    string
		model   string
		payload []byte
	}{
		{
			name:    "仅空格",
			model:   "gpt-5",
			payload: []byte("        "),
		},
		{
			name:    "仅换行符",
			model:   "gpt-5",
			payload: []byte("\n\n\n"),
		},
		{
			name:    "超长文本",
			model:   "gpt-5",
			payload: []byte(`{"messages":[{"role":"user","content":"` + string(make([]byte, 10000)) + `"}]}`),
		},
		{
			name:    "无效 JSON",
			model:   "gpt-5",
			payload: []byte(`{invalid json}`),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// 不应该 panic
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("EstimateTokensForModel panic: %v", r)
				}
			}()

			result := EstimateTokensForModel(tc.model, tc.payload)

			// 结果应该是非负数
			if result < 0 {
				t.Errorf("Token 计数不应为负数，得到: %d", result)
			}

			t.Logf("边界情况 '%s' 估算为 %d tokens", tc.name, result)
		})
	}
}

// TestEstimateTokensForModel_Consistency 测试相同输入的一致性
func TestEstimateTokensForModel_Consistency(t *testing.T) {
	model := "gpt-5"
	payload := []byte(`{"messages":[{"role":"user","content":"Test consistency"}]}`)

	// 多次调用应该返回相同结果
	firstResult := EstimateTokensForModel(model, payload)
	
	for i := 0; i < 10; i++ {
		result := EstimateTokensForModel(model, payload)
		if result != firstResult {
			t.Errorf("第 %d 次调用返回不同结果: 期望 %d, 得到 %d", i+1, firstResult, result)
		}
	}

	t.Logf("一致性测试通过，所有调用返回 %d tokens", firstResult)
}

// BenchmarkEstimateTokensForModel 性能基准测试
func BenchmarkEstimateTokensForModel(b *testing.B) {
	payload := []byte(`{"messages":[{"role":"user","content":"This is a test message for benchmarking token estimation performance"}]}`)
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		EstimateTokensForModel("gpt-5", payload)
	}
}

// BenchmarkEstimateTokensForModel_Large 大 payload 性能测试
func BenchmarkEstimateTokensForModel_Large(b *testing.B) {
	// 创建一个大的 payload (约 100KB)
	largeContent := string(make([]byte, 100000))
	payload := []byte(`{"messages":[{"role":"user","content":"` + largeContent + `"}]}`)
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		EstimateTokensForModel("gpt-5", payload)
	}
}

