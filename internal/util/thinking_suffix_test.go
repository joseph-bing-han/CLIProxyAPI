package util

import "testing"

func TestNormalizeThinkingModel_GptHyphenReasoningEffort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		in         string
		wantBase   string
		wantEffort string
		wantMeta   bool
	}{
		{name: "gpt-5.2-xhigh", in: "gpt-5.2-xhigh", wantBase: "gpt-5.2", wantEffort: "xhigh", wantMeta: true},
		{name: "gpt-5.2-high", in: "gpt-5.2-high", wantBase: "gpt-5.2", wantEffort: "high", wantMeta: true},
		{name: "gpt-5.2-medium", in: "gpt-5.2-medium", wantBase: "gpt-5.2", wantEffort: "medium", wantMeta: true},
		{name: "gpt-5.2-low", in: "gpt-5.2-low", wantBase: "gpt-5.2", wantEffort: "low", wantMeta: true},
		{name: "gpt-5.2-none", in: "gpt-5.2-none", wantBase: "gpt-5.2", wantEffort: "none", wantMeta: true},
		{name: "claude-not-affected", in: "claude-opus-4-5-thinking-high", wantBase: "claude-opus-4-5-thinking-high", wantMeta: false},
		{name: "parenthesis-still-works", in: "gpt-5.2(xhigh)", wantBase: "gpt-5.2", wantEffort: "xhigh", wantMeta: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotBase, gotMeta := NormalizeThinkingModel(tt.in)
			if gotBase != tt.wantBase {
				t.Fatalf("base model mismatch: got %q want %q", gotBase, tt.wantBase)
			}

			if !tt.wantMeta {
				if gotMeta != nil {
					t.Fatalf("expected nil metadata, got: %#v", gotMeta)
				}
				return
			}

			if gotMeta == nil {
				t.Fatalf("expected metadata, got nil")
			}

			if v, ok := gotMeta[ThinkingOriginalModelMetadataKey]; !ok {
				t.Fatalf("metadata missing %q", ThinkingOriginalModelMetadataKey)
			} else if s, ok := v.(string); !ok || s != tt.in {
				t.Fatalf("original model metadata mismatch: got %#v want %q", v, tt.in)
			}

			if v, ok := gotMeta[ReasoningEffortMetadataKey]; !ok {
				t.Fatalf("metadata missing %q", ReasoningEffortMetadataKey)
			} else if s, ok := v.(string); !ok || s != tt.wantEffort {
				t.Fatalf("reasoning effort metadata mismatch: got %#v want %q", v, tt.wantEffort)
			}
		})
	}
}
