package responses

import (
	"encoding/json"
	"testing"
)

// TestConvertOpenAIResponsesRequestToClaude_BasicMessage 测试基本消息转换
func TestConvertOpenAIResponsesRequestToClaude_BasicMessage(t *testing.T) {
	// 模拟 Codex (OpenAI Responses API) 请求
	codexRequest := []byte(`{
		"model": "gpt-5",
		"instructions": "You are a helpful assistant",
		"input": [
			{
				"type": "message",
				"role": "user",
				"content": [
					{"type": "input_text", "text": "Hello, how are you?"}
				]
			}
		],
		"max_output_tokens": 100
	}`)

	result := ConvertOpenAIResponsesRequestToClaude("claude-opus-4", codexRequest, false)

	// 验证结果不为空
	if len(result) == 0 {
		t.Fatal("期望非空结果")
	}

	// 解析结果验证结构
	var claudeReq map[string]interface{}
	if err := json.Unmarshal(result, &claudeReq); err != nil {
		t.Fatalf("无法解析转换后的 JSON: %v", err)
	}

	// 验证基本字段
	if claudeReq["model"] == nil {
		t.Error("缺少 model 字段")
	}

	// 验证 system 消息
	if system, ok := claudeReq["system"].(string); !ok || system == "" {
		t.Error("system 字段应该包含 instructions 内容")
	}

	// 验证 messages 数组
	messages, ok := claudeReq["messages"].([]interface{})
	if !ok || len(messages) == 0 {
		t.Error("messages 数组应该不为空")
	}

	// 验证 max_tokens
	if claudeReq["max_tokens"] == nil {
		t.Error("缺少 max_tokens 字段")
	}
}

// TestConvertOpenAIResponsesRequestToClaude_WithTools 测试带工具的转换
func TestConvertOpenAIResponsesRequestToClaude_WithTools(t *testing.T) {
	codexRequest := []byte(`{
		"model": "gpt-5",
		"input": [
			{
				"type": "message",
				"role": "user",
				"content": [{"type": "input_text", "text": "What's the weather?"}]
			}
		],
		"tools": [
			{
				"type": "function",
				"name": "get_weather",
				"description": "Get current weather",
				"parameters": {
					"type": "object",
					"properties": {
						"location": {"type": "string"}
					}
				}
			}
		]
	}`)

	result := ConvertOpenAIResponsesRequestToClaude("claude-opus-4", codexRequest, false)

	var claudeReq map[string]interface{}
	if err := json.Unmarshal(result, &claudeReq); err != nil {
		t.Fatalf("无法解析 JSON: %v", err)
	}

	// 验证 tools 数组
	tools, ok := claudeReq["tools"].([]interface{})
	if !ok || len(tools) == 0 {
		t.Fatal("tools 数组应该不为空")
	}

	// 验证第一个工具
	tool := tools[0].(map[string]interface{})
	if tool["name"] != "get_weather" {
		t.Errorf("工具名称不匹配，期望 get_weather，得到 %v", tool["name"])
	}

	// 验证 input_schema (从 parameters 转换而来)
	if tool["input_schema"] == nil {
		t.Error("工具应该有 input_schema 字段")
	}
}

// TestConvertOpenAIResponsesRequestToClaude_WithFunctionCall 测试包含函数调用历史的转换
func TestConvertOpenAIResponsesRequestToClaude_WithFunctionCall(t *testing.T) {
	codexRequest := []byte(`{
		"model": "gpt-5",
		"input": [
			{
				"type": "message",
				"role": "user",
				"content": [{"type": "input_text", "text": "Get weather"}]
			},
			{
				"type": "function_call",
				"call_id": "call_123",
				"name": "get_weather",
				"arguments": "{\"location\":\"Tokyo\"}"
			},
			{
				"type": "function_call_output",
				"call_id": "call_123",
				"output": "{\"temperature\":20,\"condition\":\"sunny\"}"
			}
		],
		"tools": [
			{
				"type": "function",
				"name": "get_weather",
				"parameters": {"type": "object"}
			}
		]
	}`)

	result := ConvertOpenAIResponsesRequestToClaude("claude-opus-4", codexRequest, false)

	var claudeReq map[string]interface{}
	if err := json.Unmarshal(result, &claudeReq); err != nil {
		t.Fatalf("无法解析 JSON: %v", err)
	}

	// 验证 messages 包含所有消息类型
	messages, ok := claudeReq["messages"].([]interface{})
	if !ok || len(messages) < 3 {
		t.Fatalf("messages 数组应该至少包含 3 条消息（user + assistant tool_use + user tool_result）")
	}

	// 查找 tool_use 消息
	foundToolUse := false
	foundToolResult := false
	
	for _, msg := range messages {
		m := msg.(map[string]interface{})
		if m["role"] == "assistant" {
			content, ok := m["content"].([]interface{})
			if ok && len(content) > 0 {
				c := content[0].(map[string]interface{})
				if c["type"] == "tool_use" {
					foundToolUse = true
				}
			}
		}
		if m["role"] == "user" {
			content, ok := m["content"].([]interface{})
			if ok && len(content) > 0 {
				c := content[0].(map[string]interface{})
				if c["type"] == "tool_result" {
					foundToolResult = true
				}
			}
		}
	}

	if !foundToolUse {
		t.Error("应该包含 assistant 消息与 tool_use 内容")
	}
	if !foundToolResult {
		t.Error("应该包含 user 消息与 tool_result 内容")
	}
}

// TestConvertOpenAIResponsesRequestToClaude_Streaming 测试流式请求转换
func TestConvertOpenAIResponsesRequestToClaude_Streaming(t *testing.T) {
	codexRequest := []byte(`{
		"model": "gpt-5",
		"input": [
			{
				"type": "message",
				"role": "user",
				"content": [{"type": "input_text", "text": "test"}]
			}
		],
		"stream": true
	}`)

	result := ConvertOpenAIResponsesRequestToClaude("claude-opus-4", codexRequest, true)

	var claudeReq map[string]interface{}
	if err := json.Unmarshal(result, &claudeReq); err != nil {
		t.Fatalf("无法解析 JSON: %v", err)
	}

	// 验证 stream 字段
	if stream, ok := claudeReq["stream"].(bool); !ok || !stream {
		t.Error("stream 字段应该为 true")
	}
}

// TestConvertOpenAIResponsesRequestToClaude_EmptyInput 测试空输入
func TestConvertOpenAIResponsesRequestToClaude_EmptyInput(t *testing.T) {
	codexRequest := []byte(`{
		"model": "gpt-5",
		"input": []
	}`)

	result := ConvertOpenAIResponsesRequestToClaude("claude-opus-4", codexRequest, false)

	var claudeReq map[string]interface{}
	if err := json.Unmarshal(result, &claudeReq); err != nil {
		t.Fatalf("无法解析 JSON: %v", err)
	}

	// 即使输入为空，也应该返回有效的 Claude 请求
	if claudeReq["model"] == nil {
		t.Error("应该包含 model 字段")
	}
}

// TestConvertOpenAIResponsesRequestToClaude_ReasoningEffort 测试推理级别转换
func TestConvertOpenAIResponsesRequestToClaude_ReasoningEffort(t *testing.T) {
	testCases := []struct {
		name     string
		codexReq string
	}{
		{
			name: "低推理级别",
			codexReq: `{
				"model": "gpt-5",
				"reasoning": {"effort": "low"},
				"input": [{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "test"}]}]
			}`,
		},
		{
			name: "中等推理级别",
			codexReq: `{
				"model": "gpt-5",
				"reasoning": {"effort": "medium"},
				"input": [{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "test"}]}]
			}`,
		},
		{
			name: "高推理级别",
			codexReq: `{
				"model": "gpt-5",
				"reasoning": {"effort": "high"},
				"input": [{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "test"}]}]
			}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := ConvertOpenAIResponsesRequestToClaude("claude-opus-4", []byte(tc.codexReq), false)

			var claudeReq map[string]interface{}
			if err := json.Unmarshal(result, &claudeReq); err != nil {
				t.Fatalf("无法解析 JSON: %v", err)
			}

			// 验证基本结构正确
			if claudeReq["model"] == nil {
				t.Error("应该包含 model 字段")
			}
		})
	}
}

