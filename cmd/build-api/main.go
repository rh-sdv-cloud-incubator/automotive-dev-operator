package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/rh-sdv-cloud-incubator/automotive-dev-operator/internal/buildapi"
)

func main() {
	addr := ":8080"
	if p := os.Getenv("PORT"); p != "" {
		addr = ":" + p
	}

	apiServer := buildapi.NewAPIServer(addr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-quit
		log.Println("received shutdown signal")
		cancel()
	}()

	if err := apiServer.Start(ctx); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
