#!/bin/bash

# Claude/Codex 相互转换功能测试脚本
# 用于在 rebase 后验证功能正常

set -e

echo "========================================"
echo "Claude/Codex 相互转换功能测试"
echo "========================================"
echo ""

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 测试计数
PASSED=0
FAILED=0

# 步骤 1: 编译测试
echo -e "${YELLOW}[步骤 1/5]${NC} 编译项目..."
if go build -o cli-proxy-api ./cmd/server; then
    echo -e "${GREEN}✓${NC} 编译成功"
    PASSED=$((PASSED+1))
else
    echo -e "${RED}✗${NC} 编译失败"
    FAILED=$((FAILED+1))
    exit 1
fi
echo ""

# 步骤 2: 代码格式检查
echo -e "${YELLOW}[步骤 2/5]${NC} 代码格式检查..."
gofmt -s -w .
if go vet ./...; then
    echo -e "${GREEN}✓${NC} 代码检查通过"
    PASSED=$((PASSED+1))
else
    echo -e "${RED}✗${NC} 代码检查失败"
    FAILED=$((FAILED+1))
fi
echo ""

# 步骤 3: Token 估算测试
echo -e "${YELLOW}[步骤 3/5]${NC} Token 估算功能测试..."
if go test -v ./internal/util/... -run "TestEstimate" -timeout 30s; then
    echo -e "${GREEN}✓${NC} Token 估算测试通过"
    PASSED=$((PASSED+1))
else
    echo -e "${RED}✗${NC} Token 估算测试失败"
    FAILED=$((FAILED+1))
fi
echo ""

# 步骤 4: Codex -> Claude 转换测试
echo -e "${YELLOW}[步骤 4/5]${NC} Codex -> Claude 转换测试..."
echo "测试流式响应转换、思维内容处理、工具调用等..."
if go test -v ./internal/translator/codex/claude/... -run "Streaming|Thinking|TokenCount" -timeout 30s; then
    echo -e "${GREEN}✓${NC} Codex -> Claude 转换测试通过"
    PASSED=$((PASSED+1))
else
    echo -e "${YELLOW}⚠${NC} 部分测试未通过（可能是测试用例需要调整）"
    FAILED=$((FAILED+1))
fi
echo ""

# 步骤 5: Claude -> Codex 转换测试
echo -e "${YELLOW}[步骤 5/5]${NC} Claude -> Codex 转换测试..."
echo "测试请求转换、工具处理、推理级别等..."
if go test -v ./internal/translator/claude/openai/responses/... -run "WithTools|WithFunctionCall|Streaming|ReasoningEffort" -timeout 30s; then
    echo -e "${GREEN}✓${NC} Claude -> Codex 转换测试通过"
    PASSED=$((PASSED+1))
else
    echo -e "${YELLOW}⚠${NC} 部分测试未通过（可能是测试用例需要调整）"
    FAILED=$((FAILED+1))
fi
echo ""

# 汇总结果
echo "========================================"
echo "测试结果汇总"
echo "========================================"
echo -e "通过: ${GREEN}$PASSED${NC}"
echo -e "失败: ${RED}$FAILED${NC}"
echo ""

if [ $FAILED -eq 0 ]; then
    echo -e "${GREEN}✓ 所有核心测试通过！${NC}"
    echo ""
    echo "下一步操作："
    echo "  1. 启动服务测试: ./cli-proxy-api --config config.yaml"
    echo "  2. 推送到远程仓库（如需要）: git push origin main --force-with-lease"
    exit 0
else
    echo -e "${YELLOW}⚠ 有 $FAILED 个测试失败${NC}"
    echo ""
    echo "建议操作："
    echo "  1. 检查失败的测试用例"
    echo "  2. 调整测试数据以匹配实际实现"
    echo "  3. 确认功能逻辑正确"
    exit 1
fi

