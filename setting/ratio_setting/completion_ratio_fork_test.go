package ratio_setting

import "testing"

// 本 fork: 配置的补全倍率优先于上游锁定的内置值, 没配置时仍用内置值兜底
func TestResolveCompletionRatioConfiguredWins(t *testing.T) {
	one := 1.0
	for _, name := range []string{"claude-opus-4-6", "claude-3-5-sonnet-20240620", "gpt-5", "gpt-5.4-nano-2026-03-17", "o3-pro-2025-06-10", "gpt-4o-2024-05-13"} {
		got := ResolveCompletionRatio(name, &one)
		if got.Ratio != 1 || got.Locked {
			t.Fatalf("%s: configured 1 should win, got %+v", name, got)
		}
	}
	if got := ResolveCompletionRatio("claude-opus-4-6", nil); got.Ratio != 5 || got.Locked {
		t.Fatalf("claude-opus-4-6 without config should fall back to 5 unlocked, got %+v", got)
	}
	if got := ResolveCompletionRatio("gpt-5", nil); got.Ratio != 8 {
		t.Fatalf("gpt-5 without config should fall back to 8, got %+v", got)
	}
}
