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

// kvCacheFallbackTypes lists the KV-cache quantization types OwnBot may select
// automatically when the configured type cannot serve the requested context
// inside the memory-safe budget. Entries are ordered by descending precision so
// ties keep the higher-quality cache.
var kvCacheFallbackTypes = []string{"q8_0", "q4_0"}

// contextWithKvCacheFallback chooses the KV-cache quantization type that serves
// the largest context within the memory-safe budget. The configured type is
// always tried first; when allowFallback is set, progressively smaller cache
// element types are considered so an explicitly requested context can still be
// served on machines where the default f16 cache would force a drastic context
// reduction. A candidate only wins when it strictly increases the context.
func contextWithKvCacheFallback(systemInfo ml.SystemInfo, kv ggml.KV, requestedCtx, numParallel int, configuredType string, allowFallback bool) (ctx int, kvType string, reserved, budget, bytesPerToken uint64, err error) {
	configured := strings.ToLower(strings.TrimSpace(configuredType))
	candidates := []string{configured}
	if allowFallback {
		for _, candidate := range kvCacheFallbackTypes {
			if candidate == configured {
				continue
			}
			smaller, sizeErr := kvCacheElementIsSmaller(candidate, configured)
			if sizeErr != nil || !smaller {
				continue
			}
			candidates = append(candidates, candidate)
		}
	}

	bestCtx := 0
	bestType := configured
	for _, candidate := range candidates {
		candidateCtx, candidateReserved, candidateBudget, candidateBytes, candidateErr := memorySafeContext(systemInfo, kv, requestedCtx, numParallel, candidate)
		if candidateErr != nil {
			if candidate == configured {
				return 0, "", 0, 0, 0, candidateErr
			}
			continue
		}
		if candidateCtx > bestCtx {
			bestCtx, bestType = candidateCtx, candidate
			reserved, budget, bytesPerToken = candidateReserved, candidateBudget, candidateBytes
		}
	}
	if bestCtx == 0 {
		return 0, "", 0, 0, 0, fmt.Errorf("no KV-cache type fits the available system memory")
	}
	return bestCtx, bestType, reserved, budget, bytesPerToken, nil
}

// kvCacheElementIsSmaller reports whether candidate stores fewer bytes per
// cache element than reference, using integer arithmetic to avoid rounding.
func kvCacheElementIsSmaller(candidate, reference string) (bool, error) {
	candidateBytes, candidateElements, err := kvCacheElementLayout(candidate)
	if err != nil {
		return false, err
	}
	referenceBytes, referenceElements, err := kvCacheElementLayout(reference)
	if err != nil {
		return false, err
	}
	return candidateBytes*referenceElements < referenceBytes*candidateElements, nil
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
