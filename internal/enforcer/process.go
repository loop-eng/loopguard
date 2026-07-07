package enforcer

import (
	"fmt"
	"os"
	"syscall"
)

func validatePID(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid: %d", pid)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("process %d not found: %w", pid, err)
	}
	// Signal 0 checks existence without affecting the process.
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return fmt.Errorf("process %d not accessible: %w", pid, err)
	}
	return nil
}

func sendSignal(pid int, sig syscall.Signal) error {
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		// Fall back to single process if group not available
		return syscall.Kill(pid, sig)
	}
	return syscall.Kill(-pgid, sig)
}

func stopProcess(pid int) error {
	return sendSignal(pid, syscall.SIGSTOP)
}

func contProcess(pid int) error {
	return sendSignal(pid, syscall.SIGCONT)
}

func termProcess(pid int) error {
	return sendSignal(pid, syscall.SIGTERM)
}

func killProcess(pid int) error {
	return sendSignal(pid, syscall.SIGKILL)
}
