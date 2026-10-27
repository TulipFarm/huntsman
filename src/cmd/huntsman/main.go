package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tulipfarm/huntsman/src/internal/engine"
)

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func integer(name string, fallback, min, max int) (int, error) {
	n, err := strconv.Atoi(env(name, strconv.Itoa(fallback)))
	if err != nil || n < min || n > max {
		return 0, fmt.Errorf("%s must be %d..%d", name, min, max)
	}
	return n, nil
}

func run() error {
	workers, err := integer("HUNTSMAN_WORKERS", 4, 1, 1024)
	if err != nil {
		return err
	}
	perHost, err := integer("HUNTSMAN_PER_HOST", 2, 1, 64)
	if err != nil {
		return err
	}
	capacity, err := integer("HUNTSMAN_QUEUE_CAPACITY", 10000, 1, 100000)
	if err != nil {
		return err
	}
	port, err := integer("HUNTSMAN_BROWSER_PORT", 9222, 1024, 65400)
	if err != nil {
		return err
	}
	idle, err := integer("HUNTSMAN_IDLE_MS", 200, 0, 5000)
	if err != nil {
		return err
	}
	age, err := time.ParseDuration(env("HUNTSMAN_CACHE_TTL", "1h"))
	if err != nil || age < 0 || age > 7*24*time.Hour {
		return errors.New("HUNTSMAN_CACHE_TTL must be a duration between 0 and 168h")
	}
	allowPrivate, err := strconv.ParseBool(env("HUNTSMAN_ALLOW_PRIVATE", "false"))
	if err != nil {
		return err
	}
	obeyRobots, err := strconv.ParseBool(env("HUNTSMAN_OBEY_ROBOTS", "true"))
	if err != nil {
		return err
	}
	var adblock []string
	for _, list := range strings.Split(os.Getenv("HUNTSMAN_ADBLOCK_LISTS"), ",") {
		if list = strings.TrimSpace(list); list != "" {
			if _, err := os.Stat(list); err != nil {
				return fmt.Errorf("HUNTSMAN_ADBLOCK_LISTS: %w", err)
			}
			adblock = append(adblock, list)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	store, err := engine.OpenStore(env("HUNTSMAN_DB", "data/huntsman.db"), capacity)
	if err != nil {
		return err
	}
	defer store.Close()
	svc := engine.NewService(store, age, perHost, os.Getenv("HUNTSMAN_TOKEN"))
	renderers := make([]engine.Renderer, workers)
	for i := range renderers {
		browser := engine.NewBrowser(ctx, env("HUNTSMAN_BROWSER", "lightpanda"), port+i, allowPrivate, obeyRobots, time.Duration(idle)*time.Millisecond)
		browser.AdblockLists = adblock
		renderers[i] = browser
	}
	svc.Start(ctx, renderers)
	defer svc.Close()
	addr := env("HUNTSMAN_ADDR", "127.0.0.1:8080")
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: svc.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 125 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- srv.Serve(listener) }()
	slog.Info("Huntsman ready", "address", addr, "workers", workers, "perHost", perHost, "cacheTTL", age, "allowPrivate", allowPrivate)
	select {
	case <-ctx.Done():
	case err = <-svc.Errors():
	case err = <-serverErrors:
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if shutdownErr := srv.Shutdown(shutdown); shutdownErr != nil {
		slog.Warn("HTTP shutdown", "error", shutdownErr)
		srv.Close()
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func main() {
	if err := run(); err != nil {
		slog.Error("Huntsman exited", "error", err)
		os.Exit(1)
	}
}
