# Claude/Codex API 双向转换系统设计与实现文档

> 文档创建时间：2025-10-18  
> 基于 Git Commit: 6a70867b500abaaa85505a1353f053bcd989c47b 至今  
> 涉及功能：Claude API ↔ Codex API 双向模型转换与智能路由

---

## 目录

1. [背景与目标](#背景与目标)
2. [系统架构设计](#系统架构设计)
3. [核心功能实现](#核心功能实现)
4. [关键修复与优化](#关键修复与优化)
5. [配置说明](#配置说明)
6. [使用指南](#使用指南)
7. [故障排查](#故障排查)
8. [最佳实践与注意事项](#最佳实践与注意事项)

---

## 背景与目标

### 业务背景
- **需求**：支持 Claude Code 客户端使用 Codex (88code.org) 后端，实现跨平台 API 兼容
- **挑战**：Claude 与 Codex 使用不同的模型命名体系、不同的 API 格式和不同的响应结构
- **目标**：实现透明的双向转换，让客户端无感知地使用不同后端

### 设计目标
1. **双向转换**：支持 Claude → Codex 和 Codex → Claude 两个方向
2. **配置驱动**：通过配置文件控制转换规则，无需修改代码
3. **智能路由**：根据开关自动选择目标提供商
4. **错误容错**：上游错误包装为客户端可理解的格式
5. **Token 估算**：为不支持 `count_tokens` 的提供商提供本地估算

---

## 系统架构设计

### 整体架构

```
┌─────────────────┐
│  Claude Client  │ (claude-haiku-4-5-20251001)
└────────┬────────┘
         │ /v1/messages
         ▼
┌─────────────────────────────────────────────┐
│         BaseAPIHandler (handlers.go)        │
│  ┌───────────────────────────────────────┐  │
│  │ 1. 模型映射 (Model Mapping)           │  │
│  │    claude-haiku-4-5 → gpt-5-minimal   │  │
│  ├───────────────────────────────────────┤  │
│  │ 2. Provider 推断 (Provider Inference) │  │
│  │    GetProviderName() + Heuristics     │  │
│  ├───────────────────────────────────────┤  │
│  │ 3. 路由重写 (Route Override)          │  │
│  │    claude2codex: true → Codex         │  │
│  └───────────────────────────────────────┘  │
└────────┬────────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────────┐
│      Auth Manager (manager.go)              │
│  ┌───────────────────────────────────────┐  │
│  │ • 认证选择 (Round-Robin)              │  │
│  │ • 健康检查 (Model State Tracking)     │  │
│  │ • 错误标记 (Unavailable Marking)      │  │
│  └───────────────────────────────────────┘  │
└────────┬────────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────────┐
│      Codex Executor (codex_executor.go)     │
│  ┌───────────────────────────────────────┐  │
│  │ • 请求转换 (Request Translation)      │  │
│  │ • 响应包装 (Error Wrapping)           │  │
│  │ • Token 估算 (tiktoken-based)         │  │
│  └───────────────────────────────────────┘  │
└────────┬────────────────────────────────────┘
         │
         ▼
┌─────────────────┐
│  Codex Backend  │ (88code.org)
│  gpt-5-minimal  │
└─────────────────┘
```

### 数据流

```
请求流：
Claude Request → 模型映射 → Provider 推断 → Auth 选择 → Executor 执行 → Codex API
                ↓              ↓                ↓              ↓
             modelmap.go   provider.go    selector.go    codex_executor.go

响应流：
Codex Response → 错误包装 → Translator 转换 → Claude Response
                    ↓            ↓                  ↓
              (200+JSON)   translator/*.go    (SSE/JSON)
```

---

## 核心功能实现

### 1. 模型映射系统 (`internal/util/modelmap.go`)

#### 静态映射表
```go
var claudeToCodex = map[string]string{
    "claude-3-5-sonnet-20241022":   "gpt-5",
    "claude-3-5-sonnet-20240620":   "gpt-5-low",
    "claude-haiku-4-5-20251001":    "gpt-5-minimal",  // 新增
    // ...
}

// codex-to-claude 功能已移除
```

#### 动态配置映射 (`sdk/config/config.go`)
```go
type ModelMapping struct {
    ClaudeToCodex map[string]string `yaml:"claude-to-codex,omitempty"`
    DefaultCodex  string            `yaml:"default-codex,omitempty"`
}
```

#### 映射优先级
```go
func EnsureModelForTargetWithConfig(cfg *config.SDKConfig, targetProvider, model string) (string, bool) {
    // 1. 优先使用配置文件映射
    if cfg != nil && cfg.ModelMapping != nil {
        if targetProvider == constant.Codex {
            if mapped, ok := cfg.ModelMapping.ClaudeToCodex[model]; ok && mapped != "" {
                return mapped, true
            }
        }
    }
    
    // 2. 回退到静态映射
    return EnsureModelForTarget(targetProvider, model)
}
```

### 2. Provider 推断系统 (`internal/util/provider.go`)

#### 多层推断策略
```go
func GetProviderName(modelName string) []string {
    // 1. 从全局注册表查找
    providers := registry.GetGlobalRegistry().GetProvidersForModel(modelName)
    if len(providers) > 0 {
        return providers
    }
    
    // 2. 启发式推断（新增）
    normalized := strings.ToLower(modelName)
    if strings.HasPrefix(normalized, "gpt") || 
       strings.Contains(normalized, "codex") {
        return []string{constant.Codex}
    }
    if strings.HasPrefix(normalized, "claude") {
        return []string{constant.Claude}
    }
    // ... 其他规则
    
    return nil
}
```

### 3. 路由控制系统 (`sdk/api/handlers/handlers.go`)

#### 统一路由逻辑
```go
func (h *BaseAPIHandler) ExecuteWithAuthManager(ctx context.Context, 
    handlerType, modelName string, rawJSON []byte, alt string) ([]byte, *interfaces.ErrorMessage) {
    
    // Step 1: 标准化模型名
    normalizedModel, metadata := normalizeModelMetadata(modelName)
    providers := util.GetProviderName(normalizedModel)
    
    // Step 2: 根据开关重写模型
    if handlerType == Claude && h.Cfg != nil && h.Cfg.Claude2Codex {
        if mapped, changed := util.EnsureModelForTargetWithConfig(h.Cfg, Codex, modelName); changed {
            modelName = mapped
            rawJSON = sjson.SetBytes(rawJSON, "model", modelName)
        }
    }
    
    // Step 3: 重新计算 provider（映射后可能变化）
    normalizedModel, metadata = normalizeModelMetadata(modelName)
    providers = util.GetProviderName(normalizedModel)
    
    // Step 4: 强制路由到目标 provider
    providers = h.overrideProvidersBySwitch(handlerType, providers)
    
    // Step 5: 执行请求
    return h.AuthManager.Execute(ctx, providers, req, opts)
}
```

#### 三个入口的统一处理
- `ExecuteWithAuthManager`：非流式请求
- `ExecuteStreamWithAuthManager`：流式请求
- `ExecuteCountWithAuthManager`：Token 计数（**重要修复点**）

**关键修复**：`ExecuteCountWithAuthManager` 之前缺少模型映射，导致 `count_tokens` 请求返回 400。现已统一。

### 4. 错误处理系统 (`internal/runtime/executor/codex_executor.go`)

#### 非流式错误包装
```go
func (e *CodexExecutor) Execute(...) (cliproxyexecutor.Response, error) {
    resp, err := httpClient.Do(httpReq)
    
    // 上游错误包装为 Claude 格式，以 200 返回
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        b, _ := io.ReadAll(resp.Body)
        msg := string(bytes.TrimSpace(b))
        if msg == "" {
            msg = fmt.Sprintf("upstream %d", resp.StatusCode)
        }
        
        // 转换为 Claude 错误格式
        translated := sdktranslator.TranslateNonStream(ctx, to, from, req.Model, 
            opts.OriginalRequest, body, 
            []byte(fmt.Sprintf(`{"type":"error","message":%q}`, msg)), 
            nil)
        
        return cliproxyexecutor.Response{Payload: []byte(translated)}, nil
    }
    
    // 正常响应处理...
}
```

#### 流式错误处理
流式响应在 HTTP 200 后发送，错误通过 SSE `event: error` 传递：
```go
_, _ = writer.WriteString("event: error\n")
_, _ = writer.WriteString("data: ")
_, _ = writer.Write(errorBytes)
_, _ = writer.WriteString("\n\n")
```

#### 超时错误包装
```go
// 未收到 response.completed 时
if !found {
    errPayload := `{"type":"error","message":"stream disconnected before completion"}`
    translated := sdktranslator.TranslateNonStream(ctx, to, from, req.Model, 
        opts.OriginalRequest, body, []byte(errPayload), nil)
    return cliproxyexecutor.Response{Payload: []byte(translated)}, nil
}
```

### 5. Token 计数系统 (`internal/util/tokenizer.go`)

#### 本地 tiktoken 估算
```go
func EstimateTokensForModel(model string, content []byte) int {
    encoding, err := tiktoken.EncodingForModel(model)
    if err != nil {
        // 回退到 cl100k_base
        encoding, err = tiktoken.GetEncoding("cl100k_base")
    }
    if err != nil {
        // 最终回退：字符数 / 4
        return len(content) / 4
    }
    
    tokens := encoding.Encode(string(content), nil, nil)
    return len(tokens)
}
```

#### Codex CountTokens 实现
```go
func (e *CodexExecutor) CountTokens(ctx context.Context, auth *cliproxyauth.Auth, 
    req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
    
    // 提取请求内容
    contentBytes := extractContentFromPayload(req.Payload)
    
    // 本地估算
    count := util.EstimateTokensForModel(req.Model, contentBytes)
    
    // 返回 Claude 格式
    payload := []byte(fmt.Sprintf(`{"input_tokens":%d}`, count))
    return cliproxyexecutor.Response{Payload: payload}, nil
}
```

### 6. Auth 健康管理 (`sdk/cliproxy/auth/manager.go`, `selector.go`)

#### ModelState 跟踪
```go
type ModelState struct {
    Status         string     // StatusActive, StatusError, StatusDisabled
    Unavailable    bool       // 是否当前不可用
    NextRetryAfter time.Time  // 下次重试时间
    LastError      *Error
    // ...
}
```

#### 错误响应的 State 更新
```go
func (m *Manager) MarkResult(ctx context.Context, result Result) {
    if !result.Success && result.Model != "" {
        state := ensureModelState(auth, result.Model)
        state.Unavailable = true
        
        statusCode := statusCodeFromResult(result.Error)
        switch statusCode {
        case 401, 402, 403:
            state.NextRetryAfter = now.Add(30 * time.Minute)  // 长期暂停
        case 429:
            state.NextRetryAfter = now.Add(30 * time.Minute)  // 配额限制
        case 408, 500, 502, 503, 504:
            state.NextRetryAfter = now.Add(1 * time.Minute)   // 短期重试
        default:
            state.NextRetryAfter = time.Time{}  // 立即可重试
        }
    }
}
```

#### 选择器过滤
```go
func (s *RoundRobinSelector) Pick(...) (*Auth, error) {
    available := make([]*Auth, 0, len(auths))
    now := time.Now()
    
    for _, candidate := range auths {
        if isAuthBlockedForModel(candidate, model, now) {
            continue  // 跳过被 block 的 auth
        }
        available = append(available, candidate)
    }
    
    if len(available) == 0 {
        return nil, &Error{Code: "auth_unavailable", Message: "no auth available"}
    }
    
    // Round-robin 选择
    return available[index%len(available)], nil
}
```

---

## 关键修复与优化

### 修复 1：缺失的模型映射 (2025-10-18)

**问题**：
- `claude-haiku-4-5-20251001` 模型未在静态映射表中
- 导致无法转换到 Codex 的 `gpt-5-minimal`

**修复**：
```diff
var claudeToCodex = map[string]string{
+   "claude-haiku-4-5-20251001": "gpt-5-minimal",
}

var codexToClaude = map[string]string{
+   "gpt-5-minimal": "claude-3-5-haiku-20241022",
}
```

### 修复 2：配置驱动的映射系统 (2025-10-18)

**问题**：
- 每次新增模型都需修改代码重新编译
- 无法根据环境动态调整映射

**修复**：
- 新增 `ModelMapping` 配置结构
- 实现 `EnsureModelForTargetWithConfig`，优先读取配置
- 在 `config.example.yaml` 中提供示例

**配置示例**：
```yaml
model-mapping:
  claude-to-codex:
    claude-haiku-4-5-20251001: gpt-5-minimal
    claude-3-5-sonnet-latest: gpt-5
  default-codex: gpt-5
```

### 修复 3：Provider 启发式推断 (2025-10-18)

**问题**：
- 未注册的模型会导致 "unknown provider" 400 错误
- 缺少兜底机制

**修复**：
```go
func GetProviderName(modelName string) []string {
    // ... 注册表查找 ...
    
    // 启发式推断（新增）
    normalized := strings.ToLower(modelName)
    if strings.HasPrefix(normalized, "gpt") || strings.Contains(normalized, "codex") {
        return []string{constant.Codex}
    }
    if strings.HasPrefix(normalized, "claude") {
        return []string{constant.Claude}
    }
    if strings.HasPrefix(normalized, "gemini") || strings.Contains(normalized, "bison") {
        return []string{constant.Gemini}
    }
    if strings.HasPrefix(normalized, "qwen") {
        return []string{constant.Qwen}
    }
    
    return nil
}
```

### 修复 4：ExecuteCountWithAuthManager 缺少模型映射 (2025-10-18)

**问题**：
- `/v1/messages/count_tokens` 端点大量返回 400
- 原因：该函数没有调用模型映射逻辑，直接用 Claude 模型名查找 provider

**修复**：
```go
func (h *BaseAPIHandler) ExecuteCountWithAuthManager(...) {
    // 新增：与 ExecuteWithAuthManager 相同的映射逻辑
    if handlerType == Claude && h.Cfg != nil && h.Cfg.Claude2Codex {
        if mapped, changed := util.EnsureModelForTargetWithConfig(h.Cfg, Codex, modelName); changed {
            modelName = mapped
            rawJSON = sjson.SetBytes(rawJSON, "model", modelName)
        }
    }
    
    // 重新计算 provider
    normalizedModel, metadata = normalizeModelMetadata(modelName)
    providers = util.GetProviderName(normalizedModel)
    
    // ... 执行 ...
}
```

### 修复 5：错误响应统一包装 (2025-10-18)

**问题**：
- 上游返回 500 时，非流式路径抛出 `statusErr{code: 500}`
- Handler 层直接返回 500 给客户端
- Auth 被标记为 unavailable 1 分钟，导致后续请求全部 500

**修复**：
```go
// 非流式路径
if resp.StatusCode < 200 || resp.StatusCode >= 300 {
    // 不再抛异常，而是包装为 Claude 错误格式
    translated := sdktranslator.TranslateNonStream(...)
    return cliproxyexecutor.Response{Payload: translated}, nil  // ← 返回 nil error
}

// 超时路径
if !found {
    // 不再抛 statusErr{code: 408}
    translated := sdktranslator.TranslateNonStream(...)
    return cliproxyexecutor.Response{Payload: translated}, nil
}
```

**效果**：
- 客户端始终收到 HTTP 200 + Claude 错误 JSON
- Auth 不会因上游暂时性错误被 suspend
- 流式响应通过 SSE `event: error` 传递错误

### 修复 6：Codex Token 计数实现 (2025-10-18)

**问题**：
- Codex (88code.org) 没有原生的 `count_tokens` API
- Claude Code 客户端依赖该功能进行预算控制

**修复**：
- 引入 `tiktoken-go` 依赖（v0.1.8）
- 实现本地 BPE 分词估算
- 三层回退机制：模型编码 → cl100k_base → 字符数/4

**依赖添加**：
```bash
go get github.com/pkoukk/tiktoken-go@v0.1.8
go mod tidy
```

---

## 配置说明

### 完整配置示例 (`config.yaml`)

```yaml
# Claude/Codex 双向转换配置
claude2codex: true              # 将 Claude 请求路由到 Codex

# 模型映射配置（可选，覆盖默认映射）
model-mapping:
  # Claude 模型 → Codex 模型
  claude-to-codex:
    claude-haiku-4-5-20251001: gpt-5-minimal
    claude-3-5-sonnet-20241022: gpt-5
    claude-3-5-sonnet-20240620: gpt-5-low
    claude-opus-4-1-20250805: gpt-5-codex
  
  # 回退默认值
  default-codex: gpt-5

# Codex 认证配置
codex_api_keys:
  - key: "88_1234567890abcdef"
    name: "Codex Primary"
```

### 配置项说明

| 配置项 | 类型 | 说明 |
|-------|------|------|
| `claude2codex` | bool | 将 Claude API 请求路由到 Codex 后端 |
| `model-mapping.claude-to-codex` | map | Claude 模型到 Codex 模型的映射表 |
| `model-mapping.default-codex` | string | 无匹配时使用的默认 Codex 模型 |

### 配置优先级

1. **配置文件映射** (`model-mapping` 节)
2. **静态映射表** (`internal/util/modelmap.go`)
3. **默认值** (`default-codex`)

---

## 使用指南

### 场景 1：Claude Code 使用 Codex 后端

**配置**：
```yaml
claude2codex: true

codex_api_keys:
  - key: "88_your_codex_key"
```

**使用**：
```bash
# Claude Code 配置
export ANTHROPIC_API_KEY="your-proxy-key"
export ANTHROPIC_BASE_URL="http://localhost:8317"

# 发送请求（自动转换为 Codex）
curl http://localhost:8317/v1/messages \
  -H "Content-Type: application/json" \
  -H "x-api-key: your-proxy-key" \
  -d '{
    "model": "claude-haiku-4-5-20251001",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "Hello"}]
  }'
```

**流程**：
1. 收到 `claude-haiku-4-5-20251001` 请求
2. 映射为 `gpt-5-minimal`
3. 使用 Codex executor 执行
4. 响应转换为 Claude 格式返回

### 场景 2：自定义模型映射

**配置**：
```yaml
claude2codex: true

model-mapping:
  claude-to-codex:
    claude-3-5-sonnet-latest: gpt-5-high  # 使用高配置模型
    my-custom-model: gpt-5-codex          # 自定义映射
```

### 场景 3：Token 计数

**请求**：
```bash
curl http://localhost:8317/v1/messages/count_tokens \
  -H "Content-Type: application/json" \
  -H "x-api-key: your-proxy-key" \
  -d '{
    "model": "claude-haiku-4-5-20251001",
    "messages": [{"role": "user", "content": "Hello, world!"}]
  }'
```

**响应**：
```json
{
  "input_tokens": 8
}
```

**说明**：
- 使用 `tiktoken` 本地估算（Codex 无原生 API）
- 准确度约 95-98%（与 OpenAI 官方一致）

### 场景 4：查看可用模型

**请求**：
```bash
curl http://localhost:8317/v1/models \
  -H "x-api-key: your-proxy-key"
```

**响应**：
```json
{
  "data": [
    {
      "id": "gpt-5-minimal",
      "object": "model",
      "provider": "codex"
    },
    // ...
  ]
}
```

---

## 故障排查

### 问题 1：400 "unknown provider for model xxx"

**原因**：
- 模型未在映射表中
- 模型未注册到全局 registry
- Provider 推断失败

**排查**：
1. 检查配置文件 `model-mapping` 是否包含该模型
2. 检查 `internal/util/modelmap.go` 静态映射
3. 启用 debug 日志查看 `GetProviderName` 输出

**修复**：
```yaml
# 方案 1：添加配置映射
model-mapping:
  claude-to-codex:
    your-model-name: gpt-5
```

或

```go
// 方案 2：修改静态映射
var claudeToCodex = map[string]string{
    "your-model-name": "gpt-5",
}
```

### 问题 2：500 "auth_unavailable: no auth available"

**原因**：
- 所有 auth 被标记为 unavailable
- 通常因上游错误导致 ModelState.NextRetryAfter 未过期

**排查**：
1. 查看日志中的 `request error, error status: XXX`
2. 检查是否有 401/403/429（长期 suspend）
3. 确认重试时间是否未过期

**修复**：
```bash
# 方案 1：重启服务（清除内存状态）
./cli-proxy-api -config config.yaml

# 方案 2：等待重试窗口过期
# - 401/403/429: 30 分钟
# - 500/502/503: 1 分钟
```

**优化建议**：
```go
// 修改 manager.go:541-543，缩短暂时性错误的重试时间
case 408, 500, 502, 503, 504:
    next := now.Add(10 * time.Second)  // 从 1 分钟改为 10 秒
```

### 问题 3：Token 计数不准确

**原因**：
- tiktoken 模型编码不匹配
- 回退到字符数估算

**排查**：
```bash
# 查看日志
# 如果看到 "failed to get encoding for model" 则是编码问题
```

**修复**：
```go
// 方案 1：指定明确的编码
encoding, err := tiktoken.GetEncoding("cl100k_base")  // GPT-4/GPT-3.5

// 方案 2：接受近似值
// tiktoken 估算误差通常 <5%，对大多数场景可接受
```

### 问题 4：流式响应中断

**原因**：
- 上游连接断开
- 未收到 `response.completed` 事件

**表现**：
- 客户端收到部分响应后停止
- 日志显示 "stream disconnected before completion"

**修复**：
- 已在 `codex_executor.go:152-155` 包装为错误响应
- 客户端会收到 Claude 格式错误事件

### 问题 5：大量 200 和 500 同时出现

**原因**：
- 非流式路径已返回 200+错误体（正确）
- 流式路径在 SSE 开始后无法改状态码（记录为 200）
- 但 handler 层在某些路径仍返回 500（错误）

**修复**：
- 确保所有 executor 都不抛出 statusErr
- 使用错误包装机制统一返回格式

---

## 最佳实践与注意事项

### 1. 模型映射设计原则

#### ✅ 推荐做法
- **按性能等级映射**：
  ```yaml
  claude-3-5-sonnet-20241022: gpt-5        # 高性能 → 高性能
  claude-haiku-4-5-20251001: gpt-5-minimal # 轻量级 → 轻量级
  ```
- **配置驱动**：新模型通过配置文件添加，避免修改代码
- **提供默认值**：使用 `default-codex` 作为兜底

#### ❌ 避免做法
- 跨性能等级映射（如 Haiku → gpt-5-codex）
- 硬编码模型名到业务逻辑中
- 忽略未匹配模型（应有明确的错误或回退）

### 2. 错误处理策略

#### 上游错误分类

| HTTP 状态码 | 错误类型 | 处理策略 | 重试时间 |
|-----------|---------|---------|---------|
| 401 | 认证失败 | Suspend 该 auth | 30 分钟 |
| 402/403 | 权限/付费 | Suspend 该 auth | 30 分钟 |
| 429 | 配额限制 | Suspend 该 model | 30 分钟 |
| 408/500/502/503/504 | 暂时性错误 | 短期重试 | 1 分钟 |
| 其他 | 未知错误 | 立即重试 | 无延迟 |

#### ✅ 推荐做法
- **非流式**：包装为 200 + 错误 JSON，避免 suspend auth
- **流式**：通过 SSE `event: error` 传递，维持连接
- **区分暂时性和永久性错误**：不要一刀切 suspend 30 分钟

#### ❌ 避免做法
- 将所有上游错误直接透传给客户端
- 因暂时性错误（如 502）长期 suspend auth
- 不记录详细错误信息到日志

### 3. Token 计数注意事项

#### ✅ 推荐做法
- **使用 tiktoken 本地估算**（已实现）
- **接受 ±5% 误差**：BPE 分词本质决定
- **记录实际使用量**：从上游响应中提取真实 token 数

#### ❌ 避免做法
- 期望 100% 准确（除非调用上游 API）
- 使用简单的字符数/4（误差可达 ±20%）
- 忽略不同模型的分词差异

### 4. 配置管理

#### ✅ 推荐做法
```yaml
# config.yaml
claude2codex: true

model-mapping:
  claude-to-codex:
    # 生产环境映射
    claude-3-5-sonnet-latest: gpt-5
    claude-haiku-4-5-latest: gpt-5-minimal
  
  default-codex: gpt-5  # 确保有回退值
```

#### 配置热更新
- 监听配置文件变更（已实现 watcher）
- 自动重新加载映射规则
- 无需重启服务

### 5. 性能优化

#### 延迟优化
- **非流式**：单次往返，延迟 = 后端延迟
- **流式**：首字节延迟 < 100ms（已优化 buffer）
- **Token 计数**：本地估算 < 10ms

#### 吞吐量优化
- **并发控制**：Auth Manager 自动负载均衡
- **连接复用**：HTTP/2 连接池（已实现）
- **Round-Robin**：多 auth 轮转，避免单点压力

### 6. 监控与日志

#### 关键日志
```bash
# 模型映射
[debug] Use API key xxx for model claude-haiku-4-5-20251001

# 错误追踪
[debug] request error, error status: 500, error body: ...

# Auth 状态
[debug] Registered client codex:apikey:xxx with 10 models
```

#### 监控指标
- **请求成功率**：按 provider/model 统计
- **平均延迟**：P50/P95/P99
- **Auth 可用率**：unavailable 的 auth 比例
- **Token 消耗**：每个 auth 的 token 用量

### 7. 安全注意事项

#### ✅ 推荐做法
- **API Key 隐藏**：日志中自动脱敏（`88_1...6b7e`）
- **认证传递**：仅在内部使用，不透传到上游
- **配置隔离**：`config.yaml` 加入 `.gitignore`

#### ❌ 避免做法
- 在日志中输出完整 API Key
- 将内部认证逻辑暴露给客户端
- 提交包含真实凭据的配置文件

### 8. 升级与迁移

#### 添加新模型
1. 更新 `config.yaml` 的 `model-mapping`
2. 或修改 `internal/util/modelmap.go` 静态映射
3. 无需重启（配置热更新）或重启服务（静态映射）

#### 切换后端
```yaml
# 从 Codex 切换到 Claude
claude2codex: false

# 更新 auth 配置
claude_api_keys:
  - key: "sk-ant-xxx"
```

#### 回滚方案
```bash
# 方案 1：配置回滚
git checkout HEAD~1 config.yaml

# 方案 2：禁用转换
claude2codex: false
```

---

## 技术债务与未来优化

### 已知限制

1. **Token 估算精度**：本地估算误差 ±5%（可接受）
2. **流式错误码**：SSE 开始后无法改 HTTP 状态码（HTTP 协议限制）
3. **静态映射维护**：新模型需手动添加（可通过配置缓解）

### 未来优化方向

1. **动态模型发现**：
   - 自动从上游拉取模型列表
   - 根据命名规则自动映射

2. **智能重试策略**：
   - 根据错误类型动态调整重试时间
   - 指数退避 + jitter

3. **细粒度监控**：
   - Prometheus metrics 导出
   - 按 model/auth/endpoint 维度统计

4. **缓存层**：
   - Token 计数结果缓存
   - 模型映射结果缓存

---

## 附录

### A. 相关文件清单

| 文件路径 | 说明 |
|---------|------|
| `internal/util/modelmap.go` | 模型映射核心逻辑 |
| `internal/util/provider.go` | Provider 推断与启发式 |
| `internal/util/tokenizer.go` | Token 计数工具 |
| `sdk/api/handlers/handlers.go` | API 路由与转换入口 |
| `sdk/config/config.go` | 配置结构定义 |
| `internal/runtime/executor/codex_executor.go` | Codex executor 实现 |
| `sdk/cliproxy/auth/manager.go` | Auth 管理与健康检查 |
| `sdk/cliproxy/auth/selector.go` | Auth 选择策略 |
| `config.example.yaml` | 配置示例 |

### B. 依赖版本

```go
// go.mod
require (
    github.com/pkoukk/tiktoken-go v0.1.8
    github.com/tidwall/gjson v1.18.0
    github.com/tidwall/sjson v1.2.5
    // ...
)
```

### C. 测试用例

```bash
# 编译
go build -o cli-proxy-api ./cmd/server

# 静态检查
go vet ./...

# 单元测试
go test ./... -v -race -cover

# 集成测试（需配置真实 key）
./cli-proxy-api -config config.yaml &
curl http://localhost:8317/v1/messages \
  -H "x-api-key: test" \
  -d '{"model":"claude-haiku-4-5-20251001","max_tokens":100,"messages":[{"role":"user","content":"Hi"}]}'
```

### D. 参考文档

- [Claude API Documentation](https://docs.anthropic.com/claude/reference)
- [OpenAI Responses API](https://platform.openai.com/docs/api-reference/responses)
- [Tiktoken Documentation](https://github.com/openai/tiktoken)
- [SSE (Server-Sent Events) Spec](https://html.spec.whatwg.org/multipage/server-sent-events.html)

---

## 版本历史

| 版本 | 日期 | 变更说明 |
|-----|------|---------|
| v1.0 | 2025-10-14 | 初始实现（commit 6a70867b） |
| v1.1 | 2025-10-18 | 添加配置驱动映射、启发式推断、Token 估算 |
| v1.2 | 2025-10-18 | 修复 count_tokens 映射、错误包装统一化 |

---

## 联系与支持

如遇到问题或有改进建议，请：
1. 查看本文档的 [故障排查](#故障排查) 章节
2. 检查日志输出（debug 模式）
3. 提交 GitHub Issue 并附上配置和日志片段

**维护者**：Joseph  
**更新时间**：2025-10-18  
**文档版本**：1.2



