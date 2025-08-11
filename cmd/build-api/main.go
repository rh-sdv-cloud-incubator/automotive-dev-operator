package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rh-sdv-cloud-incubator/automotive-dev-operator/internal/buildapi"
)

func main() {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(handler))
	ctrl.SetLogger(ctrl.Log.WithName("build-api"))

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
		slog.Info("received shutdown signal")
		cancel()
	}()

	if err := apiServer.Start(ctx); err != nil {
		slog.Error("server error", "error", err)
	}
}
