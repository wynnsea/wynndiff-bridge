package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"wynndiff-bridge/internal/config"
	"wynndiff-bridge/internal/server"
)

func main() {
	cfg := config.Load(".env")
	addr := cfg.BindAddr + ":" + cfg.Port

	srv := &http.Server{
		Addr:              addr,
		Handler:           server.New(cfg),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("WynnDiff Bridge listening on http://%s", addr)
		log.Printf("Target Repo : %s", cfg.RepoDir)
		if cfg.BridgeToken == "" {
			log.Printf("WARNING     : BRIDGE_TOKEN kosong (isi di .env untuk keamanan!)")
		} else {
			log.Printf("Auth        : Bearer token aktif")
		}
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	log.Println("WynnDiff Bridge stopped.")
}
