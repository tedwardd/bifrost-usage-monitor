package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

func runRefresh() {
	pidPath := sharedPIDPath()
	data, err := os.ReadFile(pidPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "refresh: daemon not running (no PID file at %s)\n", pidPath)
		os.Exit(1)
	}

	pidStr := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "refresh: invalid PID %q in %s\n", pidStr, pidPath)
		os.Exit(1)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		fmt.Fprintf(os.Stderr, "refresh: process %d not found\n", pid)
		os.Exit(1)
	}

	if err := proc.Signal(syscall.SIGUSR1); err != nil {
		fmt.Fprintf(os.Stderr, "refresh: signal %d: %v\n", pid, err)
		os.Exit(1)
	}

	fmt.Printf("refresh: signaled daemon (PID %d)\n", pid)
}
