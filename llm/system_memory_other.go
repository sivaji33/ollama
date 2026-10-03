//go:build !windows

package llm

import "fmt"

func currentFreePhysicalMemory() (uint64, error) {
	return 0, fmt.Errorf("live physical-memory verification is only implemented on Windows")
}
