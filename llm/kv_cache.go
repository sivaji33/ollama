package llm

import (
	"fmt"
	"math"
	"strings"

	"github.com/ollama/ollama/fs/ggml"
	"github.com/ollama/ollama/ml"
)

const llamaContextAlignment uint64 = 256

// memorySafeContext reserves half of currently available physical memory for
// Windows and OwnBot activity, then uses the other half as the KV-cache budget.
func memorySafeContext(systemInfo ml.SystemInfo, kv ggml.KV, requestedCtx, numParallel int, cacheType string) (ctx int, reserved, budget, bytesPerToken uint64, err error) {
	if systemInfo.FreeMemory == 0 {
		return 0, 0, 0, 0, fmt.Errorf("cannot calculate a memory-safe context without an available-memory measurement")
	}

	available := systemInfo.FreeMemory
	if systemInfo.TotalMemory > 0 && available > systemInfo.TotalMemory {
		available = systemInfo.TotalMemory
	}
	budget = available / 2
	reserved = systemInfo.TotalMemory - min(available, systemInfo.TotalMemory)
	reserved += available - budget

	bytesPerToken, err = kvCacheBytesPerToken(kv, cacheType)
	if err != nil {
		return 0, reserved, budget, 0, err
	}
	parallel := max(numParallel, 1)
	perParallelToken, ok := checkedMultiply(bytesPerToken, uint64(parallel))
	if !ok || perParallelToken == 0 {
		return 0, reserved, budget, bytesPerToken, fmt.Errorf("KV-cache size per context token overflows")
	}

	maxCtx := requestedCtx
	if trainCtx := kv.ContextLength(); trainCtx > 0 && (maxCtx <= 0 || uint64(maxCtx) > trainCtx) {
		maxCtx = int(min(trainCtx, uint64(math.MaxInt)))
	}
	if maxCtx <= 0 {
		return 0, reserved, budget, bytesPerToken, fmt.Errorf("cannot calculate a memory-safe context without a model context limit")
	}

	ctx = int(min(uint64(maxCtx), budget/perParallelToken))
	ctx = int(uint64(ctx) / llamaContextAlignment * llamaContextAlignment)
	if ctx == 0 {
		return 0, reserved, budget, bytesPerToken, fmt.Errorf("available memory does not fit the minimum llama-server context allocation")
	}
	return ctx, reserved, budget, bytesPerToken, nil
}

func kvCacheBytesPerToken(kv ggml.KV, cacheType string) (uint64, error) {
	blockCount := uint64(kv.BlockCount())
	headsPerLayer := kv.HeadCountKV()
	keyLength := uint64(kv.EmbeddingHeadCountK())
	valueLength := uint64(kv.EmbeddingHeadCountV())
	if blockCount == 0 || uint64(len(headsPerLayer)) != blockCount || keyLength == 0 || valueLength == 0 {
		return 0, fmt.Errorf("model GGUF metadata is incomplete for calculating KV-cache size")
	}

	bytesPerBlock, blockElements, err := kvCacheElementLayout(cacheType)
	if err != nil {
		return 0, err
	}

	var total uint64
	for _, heads := range headsPerLayer {
		keyElements, ok := checkedMultiply(heads, keyLength)
		if !ok {
			return 0, fmt.Errorf("model key-cache dimensions overflow")
		}
		valueElements, ok := checkedMultiply(heads, valueLength)
		if !ok {
			return 0, fmt.Errorf("model value-cache dimensions overflow")
		}
		keyBlocks := keyElements / blockElements
		if keyElements%blockElements != 0 {
			keyBlocks++
		}
		valueBlocks := valueElements / blockElements
		if valueElements%blockElements != 0 {
			valueBlocks++
		}
		cacheBlocks, ok := checkedAdd(keyBlocks, valueBlocks)
		if !ok {
			return 0, fmt.Errorf("model KV-cache size overflows")
		}
		layerBytes, ok := checkedMultiply(cacheBlocks, bytesPerBlock)
		if !ok {
			return 0, fmt.Errorf("model KV-cache size overflows")
		}
		total, ok = checkedAdd(total, layerBytes)
		if !ok {
			return 0, fmt.Errorf("model KV-cache size overflows")
		}
	}
	return total, nil
}

func kvCacheElementLayout(cacheType string) (bytesPerBlock, blockElements uint64, err error) {
	switch strings.ToLower(cacheType) {
	case "", "f16", "bf16":
		return 2, 1, nil
	case "f32":
		return 4, 1, nil
	case "q8_0":
		return 34, 32, nil
	case "q5_0":
		return 22, 32, nil
	case "q5_1":
		return 24, 32, nil
	case "q4_0":
		return 18, 32, nil
	case "q4_1":
		return 20, 32, nil
	default:
		return 0, 0, fmt.Errorf("unsupported KV-cache type %q for memory-safe context calculation", cacheType)
	}
}

func checkedMultiply(a, b uint64) (uint64, bool) {
	if a != 0 && b > math.MaxUint64/a {
		return 0, false
	}
	return a * b, true
}

func checkedAdd(a, b uint64) (uint64, bool) {
	if b > math.MaxUint64-a {
		return 0, false
	}
	return a + b, true
}

func contextForMemoryHeadroom(currentCtx int, currentFree, requiredFree, bytesPerToken uint64, numParallel int) (int, bool) {
	if currentCtx <= 0 || bytesPerToken == 0 {
		return 0, false
	}
	if currentFree >= requiredFree {
		return currentCtx, true
	}
	perParallelToken, ok := checkedMultiply(bytesPerToken, uint64(max(numParallel, 1)))
	if !ok || perParallelToken == 0 {
		return 0, false
	}

	deficit := requiredFree - currentFree
	tokensToRelease := deficit / perParallelToken
	if deficit%perParallelToken != 0 {
		tokensToRelease++
	}
	if tokensToRelease >= uint64(currentCtx) {
		return 0, false
	}

	nextCtx := uint64(currentCtx) - tokensToRelease
	nextCtx = nextCtx / llamaContextAlignment * llamaContextAlignment
	if nextCtx >= uint64(currentCtx) {
		nextCtx = (uint64(currentCtx) - 1) / llamaContextAlignment * llamaContextAlignment
	}
	return int(nextCtx), false
}

func minimumContextFitsHeadroom(currentFree, requiredFree, bytesPerToken uint64, numParallel int) bool {
	if currentFree >= requiredFree || bytesPerToken == 0 {
		return currentFree >= requiredFree
	}
	minimumContextTokens, ok := checkedMultiply(uint64(max(numParallel, 1)), llamaContextAlignment)
	if !ok {
		return false
	}
	minimumCacheBytes, ok := checkedMultiply(bytesPerToken, minimumContextTokens)
	return ok && requiredFree-currentFree <= minimumCacheBytes
}

func memoryHeadroomTarget(totalMemory, availableMemory uint64) uint64 {
	headroom := availableMemory / 2
	if totalMemory > 0 {
		headroom = min(headroom, totalMemory/8)
	}
	return headroom
}
