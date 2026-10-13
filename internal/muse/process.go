package muse

import (
	"context"
	"os/exec"
	"runtime"
	"strconv"
)

type ProcessManager struct{}

func (ProcessManager) List(ctx context.Context) (string, error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-Command",
			"Get-Process | Select-Object Id,ProcessName,CPU,WorkingSet | Format-Table -AutoSize | Out-String -Width 240")
	} else {
		cmd = exec.CommandContext(ctx, "ps", "aux")
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (ProcessManager) Kill(ctx context.Context, pid int) (string, error) {
	if runtime.GOOS == "windows" {
		out, err := exec.CommandContext(ctx, "taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").CombinedOutput()
		return string(out), err
	}
	out, err := exec.CommandContext(ctx, "kill", "-TERM", strconv.Itoa(pid)).CombinedOutput()
	return string(out), err
}
