// Command api runs the sample orders service that each environment deploys.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/EquinoxWN/ephemeral-test-envs/internal/api"
)

var commit = "unknown"

const drainTimeout = 10 * time.Second

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Error("listen failed", "err", err)
		os.Exit(1)
	}
	log.Info("listening", "addr", ln.Addr().String(), "commit", commit)
	if err := serve(ctx, ln, api.New(log)); err != nil {
		log.Error("server failed", "err", err)
		os.Exit(1)
	}
}

// serve runs until ctx ends, then returns only after in-flight requests drain.
func serve(ctx context.Context, ln net.Listener, h http.Handler) error {
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	drained := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), drainTimeout)
		defer cancel()
		drained <- srv.Shutdown(shut)
	}()
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return <-drained
}
