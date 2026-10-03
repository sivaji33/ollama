package llm

import (
	"testing"

	"github.com/ollama/ollama/fs/ggml"
	"github.com/ollama/ollama/ml"
)

func TestKVCacheBytesPerToken(t *testing.T) {
	kv := ggml.KV{
		"general.architecture":          "qwen3",
		"qwen3.block_count":             uint32(36),
		"qwen3.attention.head_count_kv": uint32(8),
		"qwen3.attention.key_length":    uint32(128),
		"qwen3.attention.value_length":  uint32(128),
	}

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
