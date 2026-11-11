package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"waves/internal/api"
	"waves/internal/config"
	"waves/internal/core"
	"waves/internal/events"
	"waves/internal/mcp"
	"waves/internal/store"
)

// UI is the embedded web app; cmd/waves sets it.
var UI fs.FS

// Version is the binary version; cmd/waves sets it.
var Version = "dev"

// openCore opens the database and builds the Core Service.
func openCore(ctx context.Context, d config.Dirs, log *slog.Logger) (*core.Service, *store.DB, *events.Bus, error) {
	if err := d.Ensure(); err != nil {
		return nil, nil, nil, err
	}
	db, err := store.Open(ctx, d.DB())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open database: %w", err)
	}
	bus := events.New()
	c := core.New(core.Options{DB: db, Bus: bus, Dirs: d, Version: Version, Log: log})
	return c, db, bus, nil
}

// serve runs the service in the foreground until SIGINT or SIGTERM.
func serve(ctx context.Context, d config.Dirs) error {
	cfg, err := config.Load(d)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)
	c, db, bus, err := openCore(ctx, d, log)
	if err != nil {
		return err
	}
	defer db.Close()
	defer bus.Close()
	ln, err := net.Listen("tcp", cfg.Addr())
	if err != nil {
		return fmt.Errorf("listen on %s: %w (is another waves running? try: waves status)", cfg.Addr(), err)
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := c.Start(ctx); err != nil {
		ln.Close()
		return err
	}
	defer c.Stop()
	srv := api.New(c, cfg.Port, UI, log)
	mh := mcp.Handler(c)
	for _, m := range []string{"GET", "POST", "DELETE"} {
		srv.Mount(m+" /mcp", mh)
	}
	hs := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second,
		BaseContext: func(net.Listener) context.Context { return ctx }}
	if err := config.WriteSecret(d.PIDFile(), []byte(fmt.Sprintf("%d\n", os.Getpid()))); err != nil {
		log.Warn("write pid file", "err", err)
	}
	defer os.Remove(d.PIDFile())
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	log.Info("waves is running", "addr", "http://"+cfg.Addr(), "version", Version)
	select {
	case <-ctx.Done():
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve: %w", err)
		}
	}
	shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := hs.Shutdown(shut); err != nil {
		log.Warn("shutdown", "err", err)
	}
	log.Info("waves stopped")
	return nil
}
