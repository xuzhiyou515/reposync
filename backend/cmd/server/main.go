package main

import (
	"log"
	"os"

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
		log.Printf("server stopped: %v", err)
		os.Exit(1)
	}
}
