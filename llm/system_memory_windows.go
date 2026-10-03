//go:build windows

package llm

import (
	"fmt"
	"syscall"
	"unsafe"
)

type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

var (
	systemMemoryKernel32 = syscall.NewLazyDLL("kernel32.dll")
	globalMemoryStatus   = systemMemoryKernel32.NewProc("GlobalMemoryStatusEx")
	memoryStatusExSize   = uint32(unsafe.Sizeof(memoryStatusEx{}))
)

func currentFreePhysicalMemory() (uint64, error) {
	status := memoryStatusEx{length: memoryStatusExSize}
	result, _, err := globalMemoryStatus.Call(uintptr(unsafe.Pointer(&status)))
	if result == 0 {
		return 0, fmt.Errorf("GlobalMemoryStatusEx failed: %w", err)
	}
	return status.availPhys, nil
}
