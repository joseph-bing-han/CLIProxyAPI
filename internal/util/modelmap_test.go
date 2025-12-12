package util

import (
	"testing"

	con "github.com/router-for-me/CLIProxyAPI/v6/internal/constant"
	sdkcfg "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

func TestEnsureModelForTarget_DefaultClaudeToCodexMapping(t *testing.T) {
	t.Parallel()

	got, changed := EnsureModelForTarget(con.Codex, "claude-opus-4-5-20251101")
	if !changed {
		t.Fatalf("expected mapping change, got changed=false (model=%q)", got)
	}
	if got != "gpt-5.2-xhigh" {
		t.Fatalf("mapped model mismatch: got %q want %q", got, "gpt-5.2-xhigh")
	}

	got, changed = EnsureModelForTarget(con.Codex, "claude-haiku-4-5-20251001")
	if !changed {
		t.Fatalf("expected haiku mapping change, got changed=false (model=%q)", got)
	}
	if got != "gpt-5.2" {
		t.Fatalf("haiku mapped model mismatch: got %q want %q", got, "gpt-5.2")
	}

	got, changed = EnsureModelForTarget(con.Codex, "claude-unknown-model")
	if !changed {
		t.Fatalf("expected default mapping change, got changed=false (model=%q)", got)
	}
	if got != "gpt-5.2" {
		t.Fatalf("default mapped model mismatch: got %q want %q", got, "gpt-5.2")
	}
}

func TestEnsureModelForTargetWithConfig_ClaudeToCodexMapping(t *testing.T) {
	t.Parallel()

	cfg := &sdkcfg.SDKConfig{
		ModelMapping: sdkcfg.ModelMapping{
			ClaudeToCodex: map[string]string{
				"claude-opus-4-5-20251101": "gpt-5.2-xhigh",
			},
			DefaultCodex: "gpt-5.2",
		},
	}

	got, changed := EnsureModelForTargetWithConfig(cfg, con.Codex, "claude-opus-4-5-20251101")
	if !changed {
		t.Fatalf("expected config mapping change, got changed=false (model=%q)", got)
	}
	if got != "gpt-5.2-xhigh" {
		t.Fatalf("config mapped model mismatch: got %q want %q", got, "gpt-5.2-xhigh")
	}

	got, changed = EnsureModelForTargetWithConfig(cfg, con.Codex, "claude-unknown-model")
	if !changed {
		t.Fatalf("expected config default mapping change, got changed=false (model=%q)", got)
	}
	if got != "gpt-5.2" {
		t.Fatalf("config default mapped model mismatch: got %q want %q", got, "gpt-5.2")
	}
}
