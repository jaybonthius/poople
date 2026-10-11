package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"poople/app"
	"poople/lib/errorlog"
	"poople/web"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stderr); err != nil {
		errorlog.Error(context.Background(), "web server stopped", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, operator io.Writer) (err error) {
	flags := flag.NewFlagSet("poople", flag.ContinueOnError)
	flags.SetOutput(operator)
	dataDir := flags.String("data-dir", "", "required application data directory")
	listen := flags.String("listen", "127.0.0.1:8080", "HTTP listen address")
	secure := flags.Bool("secure-cookie", false, "require HTTPS for session cookies (including behind a proxy)")
	uiFPS := flags.Int("ui-fps", 30, "maximum state-driven UI renders per second")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	if strings.TrimSpace(*dataDir) == "" {
		return errors.New("--data-dir is required")
	}
	if strings.TrimSpace(*listen) == "" {
		return errors.New("--listen must not be empty")
	}
	if *uiFPS <= 0 || time.Second/time.Duration(*uiFPS) == 0 {
		return errors.New("--ui-fps must allow a positive frame interval")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()
	backend, err := app.Open(ctx, *dataDir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, backend.Close()) }()
	handler, err := web.NewApp(ctx, backend, *secure, *uiFPS)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(operator, "Listening on %s\n", listener.Addr()); err != nil {
		return err
	}
	return app.Serve(ctx, backend, listener, handler)
}
