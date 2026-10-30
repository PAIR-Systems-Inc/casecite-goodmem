package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/PAIR-Systems-Inc/casecite-goodmem/server/internal/app"
	"github.com/PAIR-Systems-Inc/casecite-goodmem/server/internal/install"
	webassets "github.com/PAIR-Systems-Inc/casecite-goodmem/server/web"
)

var version = "dev"
var revision = "unknown"

func main() {
	if err := run(); err != nil {
		slog.Error("casecite stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) > 1 && os.Args[1] == "init-env" {
		if len(os.Args) != 3 {
			return fmt.Errorf("usage: casecite init-env /path/to/.env")
		}
		return install.InitEnv(os.Args[2])
	}
	if len(os.Args) > 1 && os.Args[1] == "wait" {
		if len(os.Args) < 4 || len(os.Args) > 5 {
			return fmt.Errorf("usage: casecite wait URL TIMEOUT_SECONDS [REQUIRED_MODEL]")
		}
		seconds, err := strconv.Atoi(os.Args[3])
		if err != nil || seconds < 1 || seconds > 3600 {
			return fmt.Errorf("invalid readiness timeout")
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
		defer cancel()
		model := ""
		if len(os.Args) == 5 {
			model = os.Args[4]
		}
		return install.Wait(ctx, os.Args[2], model)
	}
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
		fmt.Printf("casecite-goodmem %s (%s)\n", version, revision)
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
	if len(os.Args) > 1 && os.Args[1] == "migrate-goodmem-security" {
		defer server.Close()
		return server.MigrateGoodMemSecurity(context.Background(), os.Getenv("GOODMEM_PROVISIONING_FILE"))
	}
	if err := server.InitializeGoodMemSecurity(context.Background()); err != nil {
		server.Close()
		return err
	}
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
