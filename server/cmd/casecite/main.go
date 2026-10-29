package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/PAIR-Systems-Inc/casecite-goodmem/server/internal/app"
	webassets "github.com/PAIR-Systems-Inc/casecite-goodmem/server/web"
)

func main() {
	if err := run(); err != nil {
		slog.Error("casecite stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) > 1 && os.Args[1] == "setup-goodmem" {
		output := "data/goodmem.json"
		if len(os.Args) > 2 {
			output = os.Args[2]
		}
		if err := app.SetupWithTimeout(output); err != nil {
			return err
		}
		fmt.Println("GoodMem connection saved to", output)
		return nil
	}
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("casecite-goodmem 0.1.0")
		return nil
	}
	cfg, err := app.LoadConfig()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	db, err := app.OpenDB(ctx, cfg.DatabaseURL)
	cancel()
	if err != nil {
		return err
	}
	defer db.Close()
	server := app.New(cfg, db, webassets.Files(), slog.Default())
	server.StartWorkers()
	defer server.Close()
	httpServer := &http.Server{Addr: cfg.Address, Handler: server, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 5 * time.Minute, WriteTimeout: 5 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stopped)
	done := make(chan error, 1)
	go func() {
		slog.Info("CaseCite GoodMem listening", "address", cfg.Address)
		done <- httpServer.ListenAndServe()
	}()
	select {
	case err := <-done:
		if err != http.ErrServerClosed {
			return err
		}
	case <-stopped:
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return httpServer.Shutdown(ctx)
	}
	return nil
}
