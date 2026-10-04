package llm

import (
	"errors"
	"testing"

	"github.com/ollama/ollama/fs/ggml"
	"github.com/ollama/ollama/ml"
)

func qwen3KVDimensions() ggml.KV {
	return ggml.KV{
		"general.architecture":          "qwen3",
		"qwen3.block_count":             uint32(36),
		"qwen3.attention.head_count_kv": uint32(8),
		"qwen3.attention.key_length":    uint32(128),
		"qwen3.attention.value_length":  uint32(128),
		"qwen3.context_length":          uint32(262144),
	}
}

func TestKVCacheBytesPerToken(t *testing.T) {
	kv := qwen3KVDimensions()

	got, err := kvCacheBytesPerToken(kv, "")
	if err != nil {
		t.Fatal(err)
	}
	const want = 147456
	if got != want {
		t.Fatalf("KV-cache bytes per token = %d, want %d", got, want)
	}
}

func TestMemorySafeContextReservesHalfOfAvailableMemory(t *testing.T) {
	kv := ggml.KV{
		"general.architecture":          "qwen3",
		"qwen3.block_count":             uint32(36),
		"qwen3.attention.head_count_kv": uint32(8),
		"qwen3.attention.key_length":    uint32(128),
		"qwen3.attention.value_length":  uint32(128),
		"qwen3.context_length":          uint32(262144),
	}
	const available = uint64(2 << 30)
	ctx, reserved, budget, bytesPerToken, err := memorySafeContext(
		ml.SystemInfo{TotalMemory: 8 << 30, FreeMemory: available},
		kv,
		65536,
		1,
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	if budget != available/2 || reserved != (8<<30)-available/2 {
		t.Fatalf("budget/reserved = %d/%d, want %d/%d", budget, reserved, available/2, (8<<30)-available/2)
	}
	if ctx%int(llamaContextAlignment) != 0 || uint64(ctx)*bytesPerToken > budget || ctx >= 65536 {
		t.Fatalf("context %d does not fit budget %d or was not capped", ctx, budget)
	}
}

func TestContextForMemoryHeadroom(t *testing.T) {
	got, fits := contextForMemoryHeadroom(6144, 670<<20, 911<<20, 147456, 1)
	if fits {
		t.Fatal("context unexpectedly fits the required headroom")
	}
	if want := 4352; got != want {
		t.Fatalf("context = %d, want %d", got, want)
	}
}

func TestMinimumContextFitsHeadroomWithinItsCacheAllocation(t *testing.T) {
	if !minimumContextFitsHeadroom(1740<<20, 1754<<20, 147456, 1) {
		t.Fatal("minimum context cache should cover the measured headroom shortfall")
	}
	if minimumContextFitsHeadroom(1700<<20, 1754<<20, 147456, 1) {
		t.Fatal("minimum context cache should not cover the larger headroom shortfall")
	}
}

func TestSystemMemoryHeadroomUsesMeasuredAvailableAndTotalMemory(t *testing.T) {
	if got, want := memoryHeadroomTarget(8<<30, 2<<30), uint64(1<<30); got != want {
		t.Fatalf("headroom = %d, want %d", got, want)
	}
	if got, want := memoryHeadroomTarget(8<<30, 1<<30), uint64(512<<20); got != want {
		t.Fatalf("headroom = %d, want %d", got, want)
	}
}

func TestContextWithKvCacheFallbackQuantizesToServeLargerContext(t *testing.T) {
	ctx, kvType, _, budget, bytesPerToken, err := contextWithKvCacheFallback(
		ml.SystemInfo{TotalMemory: 8 << 30, FreeMemory: 2 << 30},
		qwen3KVDimensions(),
		65536,
		1,
		"",
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	// 1 GiB budget: f16 serves 7168, q8_0 13568, q4_0 25856 tokens.
	if kvType != "q4_0" {
		t.Fatalf("KV-cache type = %q, want q4_0", kvType)
	}
	if ctx != 25856 {
		t.Fatalf("context = %d, want 25856", ctx)
	}
	if uint64(ctx)*bytesPerToken > budget {
		t.Fatalf("context %d does not fit budget %d at %d bytes per token", ctx, budget, bytesPerToken)
	}
}

func TestContextWithKvCacheFallbackKeepsConfiguredTypeWhenItFits(t *testing.T) {
	ctx, kvType, _, _, bytesPerToken, err := contextWithKvCacheFallback(
		ml.SystemInfo{TotalMemory: 8 << 30, FreeMemory: 2 << 30},
		qwen3KVDimensions(),
		4096,
		1,
		"",
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if kvType != "" || ctx != 4096 {
		t.Fatalf("kvType/context = %q/%d, want \"\"/4096", kvType, ctx)
	}
	if uint64(ctx)*bytesPerToken != 4096*147456 {
		t.Fatalf("configured f16 cache should serve the full requested context")
	}
}

func TestContextWithKvCacheFallbackPrefersHigherPrecisionOnTies(t *testing.T) {
	ctx, kvType, _, _, _, err := contextWithKvCacheFallback(
		ml.SystemInfo{TotalMemory: 8 << 30, FreeMemory: 2 << 30},
		qwen3KVDimensions(),
		12000,
		1,
		"",
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	// f16 cannot serve 12000 tokens; q8_0 and q4_0 can both serve
	// alignment-truncated 11776, so the higher-precision cache wins the tie.
	if kvType != "q8_0" || ctx != 11776 {
		t.Fatalf("kvType/context = %q/%d, want q8_0/11776", kvType, ctx)
	}
}

func TestContextWithKvCacheFallbackRespectsExplicitConfiguration(t *testing.T) {
	if _, kvType, _, _, _, err := contextWithKvCacheFallback(
		ml.SystemInfo{TotalMemory: 8 << 30, FreeMemory: 2 << 30},
		qwen3KVDimensions(),
		65536,
		1,
		"",
		false,
	); err != nil || kvType != "" {
		t.Fatalf("disabled fallback kvType/err = %q/%v, want \"\"/nil", kvType, err)
	}

	ctx, kvType, _, _, _, err := contextWithKvCacheFallback(
		ml.SystemInfo{TotalMemory: 8 << 30, FreeMemory: 2 << 30},
		qwen3KVDimensions(),
		65536,
		1,
		"q4_0",
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if kvType != "q4_0" || ctx != 25856 {
		t.Fatalf("explicit q4_0 must not be upgraded: kvType/context = %q/%d", kvType, ctx)
	}
}

func TestKvCacheElementIsSmaller(t *testing.T) {
	cases := []struct {
		candidate string
		reference string
		want      bool
	}{
		{"q8_0", "", true},
		{"q4_0", "", true},
		{"q4_0", "q8_0", true},
		{"q8_0", "q4_0", false},
		{"q8_0", "q8_0", false},
	}
	for _, tc := range cases {
		got, err := kvCacheElementIsSmaller(tc.candidate, tc.reference)
		if err != nil {
			t.Fatalf("kvCacheElementIsSmaller(%q, %q): %v", tc.candidate, tc.reference, err)
		}
		if got != tc.want {
			t.Fatalf("kvCacheElementIsSmaller(%q, %q) = %v, want %v", tc.candidate, tc.reference, got, tc.want)
		}
	}
	if _, err := kvCacheElementIsSmaller("bogus", ""); err == nil {
		t.Fatal("unknown candidate cache type should report an error")
	}
}

func TestIsKvCacheLoadFailure(t *testing.T) {
	if !isKvCacheLoadFailure(errors.New("V cache quantization requires flash_attn")) {
		t.Fatal("flash-attention cache failure was not detected")
	}
	if !isKvCacheLoadFailure(errors.New(`error while handling argument "--cache-type-v": unsupported cache type`)) {
		t.Fatal("cache-type argument failure was not detected")
	}
	if isKvCacheLoadFailure(errors.New("failed to allocate KV cache")) {
		t.Fatal("plain allocation failures must not be treated as cache-configuration failures")
	}
	if isKvCacheLoadFailure(nil) {
		t.Fatal("nil must not be treated as a KV-cache load failure")
	}
}
