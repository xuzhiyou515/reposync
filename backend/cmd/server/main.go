package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"syscall"

	"reposync/backend/internal/app"
)

func main() {
	cfg := app.LoadConfigFromEnv()
	server, err := app.NewServer(cfg)
	if err != nil {
		log.Fatalf("create server: %v", err)
	}

	addr := cfg.HTTPAddr
	if addr == "" {
		addr = ":8080"
	}

	log.Printf("RepoSync server listening on %s", addr)
	if err := server.ListenAndServe(addr); err != nil {
		log.Printf("server stopped: %v", formatListenError(addr, err))
		os.Exit(1)
	}
}

func formatListenError(addr string, err error) error {
	if err == nil {
		return nil
	}
	if isAddrInUseError(err) {
		return fmt.Errorf("listen on %s failed: port is already in use; check whether another RepoSync instance or service is already running", addr)
	}
	return err
}

func isAddrInUseError(err error) bool {
	if err == nil {
		return false
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if errors.Is(opErr.Err, syscall.EADDRINUSE) {
			return true
		}
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "address already in use") ||
		strings.Contains(message, "only one usage of each socket address")
}
