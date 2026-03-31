package main

import (
	"errors"
	"strings"
	"syscall"
	"testing"
)

func TestFormatListenErrorForAddrInUse(t *testing.T) {
	err := formatListenError(":8080", syscall.EADDRINUSE)
	if err == nil {
		t.Fatal("expected formatted error")
	}
	message := err.Error()
	if !strings.Contains(message, ":8080") {
		t.Fatalf("expected address in error, got %q", message)
	}
	if !strings.Contains(message, "port is already in use") {
		t.Fatalf("expected port-in-use hint, got %q", message)
	}
}

func TestIsAddrInUseErrorForWindowsStyleMessage(t *testing.T) {
	err := errors.New("listen tcp :8080: bind: Only one usage of each socket address (protocol/network address/port) is normally permitted.")
	if !isAddrInUseError(err) {
		t.Fatalf("expected windows bind message to be detected")
	}
}
