// Package lifecycle runs a service's long-lived components under one
// errgroup and coordinates graceful shutdown on SIGINT/SIGTERM.
package lifecycle

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	"github.com/Varsh1l01/VENDORA/pkg/admin"
)

// Component is a long-running unit that stops when its context is cancelled.
type Component struct {
	Name string
	Run  func(ctx context.Context) error
}

// SignalContext returns a context cancelled on SIGINT/SIGTERM.
func SignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// Run starts all components and blocks until ctx is cancelled or one fails.
// onShutdown runs first when shutdown begins (e.g. mark readiness false).
func Run(ctx context.Context, onShutdown func(), components ...Component) error {
	g, gctx := errgroup.WithContext(ctx)
	for _, c := range components {
		g.Go(func() error {
			slog.Info("component starting", "component", c.Name)
			err := c.Run(gctx)
			if err != nil && !errors.Is(err, context.Canceled) {
				return fmt.Errorf("%s: %w", c.Name, err)
			}
			slog.Info("component stopped", "component", c.Name)
			return nil
		})
	}
	go func() {
		<-gctx.Done()
		slog.Info("shutdown initiated")
		if onShutdown != nil {
			onShutdown()
		}
	}()
	return g.Wait()
}

// GRPCServer returns a component serving s on addr with GracefulStop
// (bounded by timeout) on shutdown.
func GRPCServer(s *grpc.Server, addr string, timeout time.Duration) Component {
	return Component{Name: "grpc", Run: func(ctx context.Context) error {
		lis, err := net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("listen %s: %w", addr, err)
		}
		errc := make(chan error, 1)
		go func() { errc <- s.Serve(lis) }()
		slog.Info("grpc server listening", "addr", addr)
		select {
		case err := <-errc:
			return err
		case <-ctx.Done():
		}
		done := make(chan struct{})
		go func() { s.GracefulStop(); close(done) }()
		select {
		case <-done:
		case <-time.After(timeout):
			slog.Warn("graceful stop timed out, forcing")
			s.Stop()
		}
		return nil
	}}
}

// HandleHealthcheckFlag implements "<binary> -healthcheck": it probes the
// local admin /readyz and exits. Call it first thing in main.
func HandleHealthcheckFlag(adminAddr string) {
	hc := flag.Bool("healthcheck", false, "probe local readiness endpoint and exit")
	flag.Parse()
	if !*hc {
		return
	}
	addr := adminAddr
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	if err := admin.Probe("http://" + addr + "/readyz"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

// Fatal logs err and exits non-zero. Use only in main before components run.
func Fatal(msg string, err error) {
	slog.Error(msg, "error", err)
	os.Exit(1)
}
