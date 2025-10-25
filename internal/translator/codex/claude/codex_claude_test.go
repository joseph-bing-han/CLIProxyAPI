package claude

import (
	"context"
	"testing"
)

// TestConvertCodexResponseToClaudeNonStream_Success 测试正常的非流式响应转换
func TestConvertCodexResponseToClaudeNonStream_Success(t *testing.T) {
	ctx := context.Background()
	originalRequest := []byte(`{"model":"claude-opus-4","messages":[{"role":"user","content":"Hello"}],"tools":[]}`)

	// 模拟 Codex 完整响应（response.completed 事件的 JSON 内容）
	codexResponse := []byte(`{"type":"response.completed","response":{"id":"resp_123","model":"gpt-5","output":[{"type":"message","content":[{"type":"output_text","text":"Hello! How can I help you?"}]}],"usage":{"input_tokens":10,"output_tokens":8},"stop_reason":"end_turn"}}`)

	result := ConvertCodexResponseToClaudeNonStream(ctx, "gpt-5", originalRequest, nil, codexResponse, nil)

	// 验证结果不为空
	if result == "" {
		t.Fatal("期望非空结果，但得到空字符串")
	}

	t.Logf("转换结果: %s", result)

	// 验证结果包含必要字段
	expectedFields := []string{`"type":"message"`, `"role":"assistant"`, `"content"`, `"usage"`}
	for _, field := range expectedFields {
		if !contains(result, field) {
			t.Errorf("响应中缺少必要字段: %s\n完整响应: %s", field, result)
		}
	}
}

// TestConvertCodexResponseToClaudeNonStream_ErrorResponse 测试错误响应转换
func TestConvertCodexResponseToClaudeNonStream_ErrorResponse(t *testing.T) {
	ctx := context.Background()
	originalRequest := []byte(`{"model":"claude-opus-4","messages":[{"role":"user","content":"test"}]}`)

	// 模拟 Codex 错误响应（普通 JSON 而非 SSE）
	codexErrorResponse := []byte(`{"error":{"type":"insufficient_quota","message":"Insufficient credits"}}`)

	result := ConvertCodexResponseToClaudeNonStream(ctx, "gpt-5", originalRequest, nil, codexErrorResponse, nil)

	// 验证错误响应被正确转换为 Claude 格式
	if !contains(result, `"type":"error"`) {
		t.Error("错误响应应包含 type:error")
	}
	if !contains(result, "Insufficient credits") {
		t.Error("错误响应应包含原始错误消息")
	}
}

// TestConvertCodexResponseToClaudeNonStream_ToolUse 测试工具调用响应转换
func TestConvertCodexResponseToClaudeNonStream_ToolUse(t *testing.T) {
	ctx := context.Background()
	originalRequest := []byte(`{"model":"claude-opus-4","messages":[{"role":"user","content":"What's the weather?"}],"tools":[{"name":"get_weather","description":"Get weather"}]}`)

	// 模拟包含工具调用的 Codex 响应
	codexResponse := []byte(`{"type":"response.completed","response":{"id":"resp_456","model":"gpt-5","output":[{"type":"function_call","call_id":"call_123","name":"get_weather","arguments":"{\"location\":\"Tokyo\"}"}],"usage":{"input_tokens":15,"output_tokens":12},"stop_reason":"tool_use"}}`)

	result := ConvertCodexResponseToClaudeNonStream(ctx, "gpt-5", originalRequest, nil, codexResponse, nil)

	if result == "" {
		t.Fatal("期望非空结果")
	}

	t.Logf("工具调用转换结果: %s", result)

	// 验证工具调用相关字段
	expectedFields := []string{`"type":"tool_use"`, `"name":"get_weather"`, `"stop_reason":"tool_use"`}
	for _, field := range expectedFields {
		if !contains(result, field) {
			t.Errorf("工具调用响应中缺少必要字段: %s\n完整响应: %s", field, result)
		}
	}
}

// TestConvertCodexResponseToClaude_StreamingEvents 测试流式响应事件转换
func TestConvertCodexResponseToClaude_StreamingEvents(t *testing.T) {
	ctx := context.Background()
	originalRequest := []byte(`{"model":"claude-opus-4","messages":[{"role":"user","content":"test"}]}`)

	testCases := []struct {
		name           string
		codexEvent     []byte
		expectedEvent  string
		shouldNotEmpty bool
	}{
		{
			name:           "response.created 事件",
			codexEvent:     []byte(`data: {"type":"response.created","response":{"id":"resp_123","model":"gpt-5"}}`),
			expectedEvent:  "message_start",
			shouldNotEmpty: true,
		},
		{
			name:           "response.content_part.added 事件",
			codexEvent:     []byte(`data: {"type":"response.content_part.added","output_index":0}`),
			expectedEvent:  "content_block_start",
			shouldNotEmpty: true,
		},
		{
			name:           "response.output_text.delta 事件",
			codexEvent:     []byte(`data: {"type":"response.output_text.delta","output_index":0,"delta":"Hello"}`),
			expectedEvent:  "content_block_delta",
			shouldNotEmpty: true,
		},
		{
			name:           "response.completed 事件",
			codexEvent:     []byte(`data: {"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":5}}}`),
			expectedEvent:  "message_delta",
			shouldNotEmpty: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var param any
			results := ConvertCodexResponseToClaude(ctx, "gpt-5", originalRequest, nil, tc.codexEvent, &param)

			if tc.shouldNotEmpty && len(results) == 0 {
				t.Errorf("期望非空结果，但得到空数组")
				return
			}

			if len(results) > 0 && !contains(results[0], tc.expectedEvent) {
				t.Errorf("期望包含事件 %s，但实际结果: %s", tc.expectedEvent, results[0])
			}
		})
	}
}

// TestConvertCodexResponseToClaude_ThinkingContent 测试思维内容转换
func TestConvertCodexResponseToClaude_ThinkingContent(t *testing.T) {
	ctx := context.Background()
	originalRequest := []byte(`{"model":"claude-opus-4","messages":[{"role":"user","content":"test"}]}`)

	testCases := []struct {
		name         string
		codexEvent   []byte
		expectedType string
	}{
		{
			name:         "thinking 开始",
			codexEvent:   []byte(`data: {"type":"response.reasoning_summary_part.added","output_index":0}`),
			expectedType: "thinking",
		},
		{
			name:         "thinking 内容增量",
			codexEvent:   []byte(`data: {"type":"response.reasoning_summary_text.delta","output_index":0,"delta":"Let me think..."}`),
			expectedType: "thinking_delta",
		},
		{
			name:         "thinking 完成",
			codexEvent:   []byte(`data: {"type":"response.reasoning_summary_part.done","output_index":0}`),
			expectedType: "content_block_stop",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var param any
			results := ConvertCodexResponseToClaude(ctx, "gpt-5", originalRequest, nil, tc.codexEvent, &param)

			if len(results) == 0 {
				t.Error("期望非空结果")
				return
			}

			if !contains(results[0], tc.expectedType) {
				t.Errorf("期望包含类型 %s，但实际结果: %s", tc.expectedType, results[0])
			}
		})
	}
}

// TestBuildReverseMapFromClaudeOriginalShortToOriginal 测试工具名称映射构建
func TestBuildReverseMapFromClaudeOriginalShortToOriginal(t *testing.T) {
	originalRequest := []byte(`{"tools":[{"name":"get_current_weather","description":"Get weather"},{"name":"search_web","description":"Search"}]}`)

	reverseMap := buildReverseMapFromClaudeOriginalShortToOriginal(originalRequest)

	// 验证映射不为空（如果工具名称被缩短了）
	t.Logf("反向映射结果: %+v", reverseMap)
}

// TestClaudeTokenCount 测试 token 计数格式化
func TestClaudeTokenCount(t *testing.T) {
	ctx := context.Background()

	result := ClaudeTokenCount(ctx, 42)

	expected := `{"input_tokens":42}`
	if result != expected {
		t.Errorf("期望 %s，但得到 %s", expected, result)
	}
}

// 辅助函数：检查字符串是否包含子串
func contains(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr || len(s) > len(substr) && hasSubstring(s, substr))
}

func hasSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
