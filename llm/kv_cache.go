package llm

import (
	"fmt"
	"math"
	"strings"

	"github.com/ollama/ollama/fs/ggml"
	"github.com/ollama/ollama/ml"
)

const llamaContextAlignment uint64 = 256

// minimumUsableContext prevents the Windows memory-safety calculation from
// starting llama-server with an unusably small context such as 256 tokens.
//
// 3840 is the smallest 256-aligned context that can accommodate the observed
// request:
//
//	1653 prompt tokens + 2048 generation tokens = 3701 tokens
//
// This value is deliberately below the model's normal 4096 context target,
// while still being large enough for the observed chat workload.
const minimumUsableContext = 3840

// memorySafeContext reserves half of currently available physical memory for
// Windows and OwnBot activity, then uses the other half as the KV-cache budget.
//
// The calculated context is never silently reduced below minimumUsableContext.
// If the available memory cannot safely provide that minimum, the function
// returns an error instead of allowing llama-server to start with an unusable
// context such as 256.
func memorySafeContext(systemInfo ml.SystemInfo, kv ggml.KV, requestedCtx, numParallel int, cacheType string) (ctx int, reserved, budget, bytesPerToken uint64, err error) {
	if systemInfo.FreeMemory == 0 {
		return 0, 0, 0, 0, fmt.Errorf("cannot calculate a memory-safe context without an available-memory measurement")
	}

	available := systemInfo.FreeMemory
	if systemInfo.TotalMemory > 0 && available > systemInfo.TotalMemory {
		available = systemInfo.TotalMemory
	}

	budget = available / 2

	reserved = 0
	if systemInfo.TotalMemory > 0 {
		reserved = systemInfo.TotalMemory - min(available, systemInfo.TotalMemory)
	}
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

	if trainCtx := kv.ContextLength(); trainCtx > 0 &&
		(maxCtx <= 0 || uint64(maxCtx) > trainCtx) {
		maxCtx = int(min(trainCtx, uint64(math.MaxInt)))
	}

	if maxCtx <= 0 {
		return 0, reserved, budget, bytesPerToken,
			fmt.Errorf("cannot calculate a memory-safe context without a model context limit")
	}

	// Calculate the maximum context that the KV-cache budget can support.
	memoryCtx := int(min(uint64(maxCtx), budget/perParallelToken))

	// llama-server context is handled in 256-token increments.
	memoryCtx = alignContextDown(memoryCtx)

	if memoryCtx == 0 {
		return 0, reserved, budget, bytesPerToken,
			fmt.Errorf(
				"available memory does not fit the minimum llama-server context allocation: "+
					"available=%d bytes, KV budget=%d bytes, KV bytes/token=%d, parallel=%d",
				available,
				budget,
				bytesPerToken,
				parallel,
			)
	}

	// Never silently configure an unusably small context.
	minimumCtx := minimumUsableContext

	// If the model itself has a smaller context limit, use the model limit.
	if minimumCtx > maxCtx {
		minimumCtx = alignContextDown(maxCtx)
	}

	if minimumCtx < int(llamaContextAlignment) {
		return 0, reserved, budget, bytesPerToken,
			fmt.Errorf(
				"model context limit %d is below the minimum llama-server alignment",
				maxCtx,
			)
	}

	// If the memory calculation says only 256/512/1024/2048 etc. fit,
	// do not silently start the server at that context. The caller can try
	// a smaller KV-cache type through contextWithKvCacheFallback().
	if memoryCtx < minimumCtx {
		return 0, reserved, budget, bytesPerToken,
			fmt.Errorf(
				"available memory supports only %d context tokens with KV cache type %q, "+
					"but at least %d context tokens are required; try a smaller KV-cache type "+
					"or reduce memory usage",
				memoryCtx,
				normalizeKVCacheType(cacheType),
				minimumCtx,
			)
	}

	// Preserve the caller's requested context when it fits.
	if requestedCtx > 0 && requestedCtx < minimumCtx {
		ctx = alignContextUp(requestedCtx)

		if ctx > memoryCtx {
			return 0, reserved, budget, bytesPerToken,
				fmt.Errorf(
					"requested context %d cannot fit the memory-safe KV-cache budget; "+
						"maximum safe context is %d",
					requestedCtx,
					memoryCtx,
				)
		}

		return ctx, reserved, budget, bytesPerToken, nil
	}

	ctx = min(memoryCtx, maxCtx)
	ctx = alignContextDown(ctx)

	if ctx < minimumCtx {
		return 0, reserved, budget, bytesPerToken,
			fmt.Errorf(
				"memory-safe context %d is below the minimum usable context %d",
				ctx,
				minimumCtx,
			)
	}

	return ctx, reserved, budget, bytesPerToken, nil
}

// kvCacheFallbackTypes lists the KV-cache quantization types OwnBot may select
// automatically when the configured type cannot serve the requested context
// inside the memory-safe budget.
//
// Entries are ordered by descending precision so ties keep the higher-quality
// cache.
var kvCacheFallbackTypes = []string{
	"q8_0",
	"q4_0",
}

// contextWithKvCacheFallback chooses the KV-cache quantization type that serves
// the largest context within the memory-safe budget.
//
// The configured type is always tried first. When allowFallback is enabled,
// progressively smaller cache element types are considered.
//
// Unlike the previous implementation, a candidate that can only provide a
// tiny context such as 256 tokens is rejected. This prevents llama-server from
// starting successfully with a context that cannot service a normal chat
// request.
func contextWithKvCacheFallback(
	systemInfo ml.SystemInfo,
	kv ggml.KV,
	requestedCtx,
	numParallel int,
	configuredType string,
	allowFallback bool,
) (ctx int, kvType string, reserved, budget, bytesPerToken uint64, err error) {
	configured := normalizeKVCacheType(configuredType)

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
		candidateCtx,
			candidateReserved,
			candidateBudget,
			candidateBytes,
			candidateErr := memorySafeContext(
			systemInfo,
			kv,
			requestedCtx,
			numParallel,
			candidate,
		)

		if candidateErr != nil {
			if candidate == configured {
				// If fallback is disabled, preserve the configured cache
				// configuration and return the actual reason it cannot fit.
				if !allowFallback {
					return 0, "", 0, 0, 0, candidateErr
				}

				// If fallback is enabled, continue trying smaller cache types.
				continue
			}

			continue
		}

		if candidateCtx > bestCtx {
			bestCtx = candidateCtx
			bestType = candidate
			reserved = candidateReserved
			budget = candidateBudget
			bytesPerToken = candidateBytes
		}
	}

	if bestCtx == 0 {
		return 0, "", 0, 0, 0,
			fmt.Errorf(
				"no KV-cache type can provide at least %d context tokens within the available system memory",
				minimumUsableContext,
			)
	}

	return bestCtx, bestType, reserved, budget, bytesPerToken, nil
}

// normalizeKVCacheType normalizes the configured cache type while preserving
// the empty string used by Ollama to represent the default f16/bf16 cache.
func normalizeKVCacheType(cacheType string) string {
	return strings.ToLower(strings.TrimSpace(cacheType))
}

// alignContextDown rounds a context size down to the llama-server alignment.
func alignContextDown(ctx int) int {
	if ctx <= 0 {
		return 0
	}

	alignment := int(llamaContextAlignment)

	if alignment <= 0 {
		return ctx
	}

	return (ctx / alignment) * alignment
}

// alignContextUp rounds a context size up to the llama-server alignment.
func alignContextUp(ctx int) int {
	if ctx <= 0 {
		return 0
	}

	alignment := int(llamaContextAlignment)

	if alignment <= 0 {
		return ctx
	}

	if ctx%alignment == 0 {
		return ctx
	}

	return ((ctx / alignment) + 1) * alignment
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

	left, ok := checkedMultiply(candidateBytes, referenceElements)
	if !ok {
		return false, fmt.Errorf("KV-cache element size comparison overflows")
	}

	right, ok := checkedMultiply(referenceBytes, candidateElements)
	if !ok {
		return false, fmt.Errorf("KV-cache element size comparison overflows")
	}

	return left < right, nil
}

// kvCacheBytesPerToken calculates the KV-cache bytes consumed by one context
// token across all transformer layers.
func kvCacheBytesPerToken(kv ggml.KV, cacheType string) (uint64, error) {
	blockCount := uint64(kv.BlockCount())
	headsPerLayer := kv.HeadCountKV()
	keyLength := uint64(kv.EmbeddingHeadCountK())
	valueLength := uint64(kv.EmbeddingHeadCountV())

	if blockCount == 0 ||
		uint64(len(headsPerLayer)) != blockCount ||
		keyLength == 0 ||
		valueLength == 0 {
		return 0, fmt.Errorf(
			"model GGUF metadata is incomplete for calculating KV-cache size",
		)
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
	switch normalizeKVCacheType(cacheType) {
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
		return 0, 0,
			fmt.Errorf(
				"unsupported KV-cache type %q for memory-safe context calculation",
				cacheType,
			)
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

func contextForMemoryHeadroom(
	currentCtx int,
	currentFree,
	requiredFree,
	bytesPerToken uint64,
	numParallel int,
) (int, bool) {
	if currentCtx <= 0 || bytesPerToken == 0 {
		return 0, false
	}

	if currentFree >= requiredFree {
		return currentCtx, true
	}

	perParallelToken, ok := checkedMultiply(
		bytesPerToken,
		uint64(max(numParallel, 1)),
	)
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
		nextCtx = (uint64(currentCtx) - 1) /
			llamaContextAlignment *
			llamaContextAlignment
	}

	return int(nextCtx), false
}

func minimumContextFitsHeadroom(
	currentFree,
	requiredFree,
	bytesPerToken uint64,
	numParallel int,
) bool {
	if currentFree >= requiredFree || bytesPerToken == 0 {
		return currentFree >= requiredFree
	}

	minimumContextTokens, ok := checkedMultiply(
		uint64(max(numParallel, 1)),
		llamaContextAlignment,
	)
	if !ok {
		return false
	}

	minimumCacheBytes, ok := checkedMultiply(
		bytesPerToken,
		minimumContextTokens,
	)

	return ok && requiredFree-currentFree <= minimumCacheBytes
}

func memoryHeadroomTarget(totalMemory, availableMemory uint64) uint64 {
	headroom := availableMemory / 2

	if totalMemory > 0 {
		headroom = min(headroom, totalMemory/8)
	}

	return headroom
}
