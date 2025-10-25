# Claude/Codex 相互转换功能测试指南

## 概述

本文档说明如何测试 Claude 和 Codex (OpenAI Responses API) 之间的相互转换功能。

## 快速开始

### 自动化测试（推荐）

每次 rebase 上游代码后，运行以下命令进行完整测试：

```bash
./test-claude-codex-conversion.sh
```

该脚本会自动执行以下测试：
1. ✅ **编译测试** - 确保代码无语法错误
2. ✅ **代码检查** - 运行 `gofmt` 和 `go vet`
3. ✅ **Token 估算测试** - 验证 token 计数功能
4. ✅ **Codex -> Claude 转换** - 测试响应格式转换
5. ✅ **Claude -> Codex 转换** - 测试请求格式转换

### 手动测试

#### 1. 编译项目

```bash
go build -o cli-proxy-api ./cmd/server
```

#### 2. 运行单元测试

```bash
# 测试 Token 估算
go test -v ./internal/util/... -run "TestEstimate"

# 测试 Codex -> Claude 转换
go test -v ./internal/translator/codex/claude/...

# 测试 Claude -> Codex 转换
go test -v ./internal/translator/claude/openai/responses/...
```

#### 3. 运行服务并手动测试

```bash
# 启动服务
./cli-proxy-api --config config.yaml

# 使用 Claude API 格式发送请求（会自动转换为 Codex）
curl -X POST http://localhost:8080/v1/messages \
  -H "Content-Type: application/json" \
  -H "x-api-key: YOUR_AUTH_KEY" \
  -d '{
    "model": "claude-opus-4",
    "messages": [{"role": "user", "content": "Hello"}],
    "max_tokens": 100
  }'
```

## 测试覆盖范围

### Token 估算测试 (`internal/util/tokenizer_test.go`)

- ✅ 多种模型的 token 计数（GPT-5, GPT-4o, Claude Opus, GPT-3.5）
- ✅ UTF-8 字符处理（中文、日文、emoji、混合语言）
- ✅ 边界情况（空输入、超长文本、无效 JSON）
- ✅ 计算一致性验证
- ✅ 性能基准测试

### Codex -> Claude 转换测试 (`internal/translator/codex/claude/codex_claude_test.go`)

**流式响应转换：**
- ✅ `response.created` 事件 → `message_start`
- ✅ `response.content_part.added` → `content_block_start`
- ✅ `response.output_text.delta` → `content_block_delta`
- ✅ `response.completed` → `message_delta` + `message_stop`

**思维内容处理：**
- ✅ `response.reasoning_summary_part.added` → thinking 开始
- ✅ `response.reasoning_summary_text.delta` → thinking 增量
- ✅ `response.reasoning_summary_part.done` → thinking 完成

**错误处理：**
- ✅ 错误响应转换为 Claude 错误格式
- ✅ 保持原始错误消息

**其他功能：**
- ✅ 工具名称反向映射
- ✅ Token 计数格式化

### Claude -> Codex 转换测试 (`internal/translator/claude/openai/responses/claude_codex_test.go`)

**基本功能：**
- ✅ 基本消息转换
- ✅ System 消息处理
- ✅ 流式/非流式请求
- ✅ 空输入处理

**工具调用：**
- ✅ 工具定义转换（`tools` → Codex format）
- ✅ `parameters` → `input_schema`
- ✅ 函数调用历史转换
- ✅ `tool_use` → `function_call`
- ✅ `tool_result` → `function_call_output`

**推理级别：**
- ✅ 低推理级别（`reasoning.effort: low`）
- ✅ 中等推理级别（`reasoning.effort: medium`）
- ✅ 高推理级别（`reasoning.effort: high`）

## 核心功能验证

### 1. 错误响应统一处理

**特性：** Codex 错误响应被包装为统一格式返回（HTTP 200 + 错误 JSON）

测试方法：
```go
// 测试文件: codex_claude_test.go
func TestConvertCodexResponseToClaudeNonStream_ErrorResponse(t *testing.T)
```

验证：
- ✅ 错误响应包含 `"type":"error"`
- ✅ 保留原始错误消息
- ✅ 设置合适的错误类型

### 2. 流式响应错误处理

**特性：** 流式响应中的错误被包装为 SSE 错误事件

实现位置：`internal/runtime/executor/codex_executor.go`

验证：
- ✅ HTTP 错误状态码转换为 SSE `event: error`
- ✅ 错误消息格式正确
- ✅ 连接断开前完成发送

### 3. Token 计数本地估算

**特性：** 使用 `tiktoken-go` 进行本地 token 估算

测试验证：
- ✅ 支持多种模型（GPT-5, GPT-4o, GPT-3.5, Claude）
- ✅ UTF-8 字符正确处理
- ✅ 边界情况回退策略
- ✅ 计算结果一致性

## 测试结果示例

```
========================================
Claude/Codex 相互转换功能测试
========================================

[步骤 1/5] 编译项目...
✓ 编译成功

[步骤 2/5] 代码格式检查...
✓ 代码检查通过

[步骤 3/5] Token 估算功能测试...
✓ Token 估算测试通过

[步骤 4/5] Codex -> Claude 转换测试...
✓ Codex -> Claude 转换测试通过

[步骤 5/5] Claude -> Codex 转换测试...
✓ Claude -> Codex 转换测试通过

========================================
测试结果汇总
========================================
通过: 5
失败: 0

✓ 所有核心测试通过！
```

## Rebase 后的测试流程

1. **解决冲突**
   ```bash
   git rebase upstream/main
   # 解决冲突后
   git add .
   git rebase --continue
   ```

2. **运行完整测试**
   ```bash
   ./test-claude-codex-conversion.sh
   ```

3. **验证功能**
   - 如果所有测试通过，功能正常
   - 如果有测试失败，检查：
     - 上游是否修改了相关接口
     - 测试用例是否需要更新
     - 实现逻辑是否需要调整

4. **提交并推送**
   ```bash
   git push origin main --force-with-lease
   ```

## 故障排查

### 编译失败

**问题：** `imported and not used` 或 `redeclared`

**解决：**
```bash
# 自动格式化和清理未使用的导入
gofmt -s -w .
go mod tidy
```

### 测试失败

**问题：** 转换测试失败

**排查步骤：**
1. 查看失败的测试输出
2. 检查实际转换结果
3. 对比期望格式和实际格式
4. 更新测试用例或修复转换逻辑

### 功能异常

**问题：** 服务运行但转换不正确

**排查步骤：**
1. 启用调试日志
2. 查看 `logs/` 目录下的请求/响应日志
3. 对比输入和输出格式
4. 检查翻译器注册是否正确

## 性能考虑

- Token 估算使用本地库，无需网络请求
- 转换器使用 `gjson`/`sjson` 高效处理 JSON
- 流式响应使用 channel 异步处理
- 错误处理不阻塞主流程

## 维护建议

1. **每次 rebase 后运行测试脚本**
2. **有新功能时添加对应测试用例**
3. **定期更新测试数据以匹配最新 API**
4. **保持测试覆盖率 > 80%**

## 相关文件

- 测试脚本：`test-claude-codex-conversion.sh`
- 转换器实现：
  - `internal/translator/codex/claude/` - Codex → Claude
  - `internal/translator/claude/openai/responses/` - Claude → Codex
- Token 估算：`internal/util/tokenizer.go`
- 执行器：`internal/runtime/executor/codex_executor.go`

## 参考文档

- [Claude API 文档](https://docs.anthropic.com/claude/reference)
- [OpenAI Responses API](https://platform.openai.com/docs/api-reference/responses)
- [主项目 README](../README_CN.md)

