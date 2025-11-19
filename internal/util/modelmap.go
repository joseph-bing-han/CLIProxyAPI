package util

import (
	"strings"

	con "github.com/router-for-me/CLIProxyAPI/v6/internal/constant"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	sdkcfg "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

// TargetHasModel 判断目标提供方是否包含给定模型名, 优先使用运行时注册表, 其次使用静态清单
func TargetHasModel(target string, model string) bool {
	if model == "" || target == "" {
		return false
	}
	t := strings.ToLower(strings.TrimSpace(target))
	name := strings.ToLower(strings.TrimSpace(model))

	// 优先: 动态注册表 (按 provider 精确匹配)
	if providers := registry.GetGlobalRegistry().GetModelProviders(model); len(providers) > 0 {
		for i := range providers {
			if strings.EqualFold(providers[i], t) {
				return true
			}
		}
	}

	// 回退: 静态清单
	switch t {
	case con.Codex:
		for _, m := range registry.GetOpenAIModels() {
			if m != nil && strings.EqualFold(m.ID, name) {
				return true
			}
		}
	case con.Claude:
		for _, m := range registry.GetClaudeModels() {
			if m != nil && strings.EqualFold(m.ID, name) {
				return true
			}
		}
	}
	return false
}

// EnsureModelForTarget 在切换路由时保证模型名可用于目标提供方:
// 1) 若目标已有该模型则直通返回
// 2) 否则根据映射表回退到最接近模型
func EnsureModelForTarget(target, model string) (string, bool) {
	if TargetHasModel(target, model) {
		return model, false
	}

	// 统一小写进行匹配
	t := strings.ToLower(strings.TrimSpace(target))
	m := strings.ToLower(strings.TrimSpace(model))

	// Claude -> Codex (静态默认)
	claudeToCodex := map[string]string{
		"claude-opus-4-1-20250805":   "gpt-5.1-codex-max-xhigh",
		"claude-sonnet-4-5-20250929": "gpt-5.1-codex-max-high",
		"claude-sonnet-4-20250514":   "gpt-5.1-codex-max-medium",
		"claude-3-7-sonnet-20250219": "gpt-5.1-codex-max-medium",
		"claude-3-5-haiku-20241022":  "gpt-5.1-codex-max",
		"claude-haiku-4-5-20251001":  "gpt-5.1-codex-max",
	}

	// Codex -> Claude (静态默认)
	codexToClaude := map[string]string{
		"gpt-5-high":         "claude-opus-4-1-20250805",
		"gpt-5-medium":       "claude-sonnet-4-5-20250929",
		"gpt-5-low":          "claude-3-7-sonnet-20250219",
		"gpt-5-minimal":      "claude-3-5-haiku-20241022",
		"gpt-5":              "claude-opus-4-1-20250805",
		"gpt-5-codex":        "claude-sonnet-4-5-20250929",
		"gpt-5-codex-low":    "claude-3-5-haiku-20241022",
		"gpt-5-codex-medium": "claude-sonnet-4-5-20250929",
		"gpt-5-codex-high":   "claude-opus-4-1-20250805",
	}

	switch t {
	case con.Codex:
		if mapped, ok := claudeToCodex[m]; ok {
			return mapped, true
		}
		// 未知 Claude -> Codex 回退
		return "gpt-5", true
	case con.Claude:
		if mapped, ok := codexToClaude[m]; ok {
			return mapped, true
		}
		// 未知 Codex -> Claude 回退
		return "claude-sonnet-4-5-20250929", true
	default:
		return model, false
	}
}

// EnsureModelForTargetWithConfig 在切换路由时, 优先使用配置中的模型映射; 否则退回静态映射
func EnsureModelForTargetWithConfig(cfg *sdkcfg.SDKConfig, target, model string) (string, bool) {
	if TargetHasModel(target, model) {
		return model, false
	}
	t := strings.ToLower(strings.TrimSpace(target))
	m := strings.ToLower(strings.TrimSpace(model))

	// 尝试读取配置映射
	if cfg != nil {
		if t == con.Codex && len(cfg.ModelMapping.ClaudeToCodex) > 0 {
			if mapped, ok := cfg.ModelMapping.ClaudeToCodex[m]; ok && strings.TrimSpace(mapped) != "" {
				return mapped, true
			}
			if def := strings.TrimSpace(cfg.ModelMapping.DefaultCodex); def != "" {
				return def, true
			}
		}
		if t == con.Claude && len(cfg.ModelMapping.CodexToClaude) > 0 {
			if mapped, ok := cfg.ModelMapping.CodexToClaude[m]; ok && strings.TrimSpace(mapped) != "" {
				return mapped, true
			}
			if def := strings.TrimSpace(cfg.ModelMapping.DefaultClaude); def != "" {
				return def, true
			}
		}
	}

	// 回退到静态逻辑
	return EnsureModelForTarget(target, model)
}
