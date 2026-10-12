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
	"strconv"
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
	trustedProxy := flags.Bool("trusted-proxy", false, "take client addresses for rate limits from the last X-Forwarded-For entry (only behind exactly one reverse proxy)")
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
	var options []web.Option
	if *trustedProxy {
		options = append(options, web.WithTrustedProxy())
	}
	handler, token, err := web.NewApp(ctx, backend, *secure, *uiFPS, options...)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(operator, "Listening on %s\n", listener.Addr()); err != nil {
		return err
	}
	if token != "" {
		if _, err := fmt.Fprintf(operator, "Admin setup (15 minutes): %s/setup?token=%s\n", localOrigin(listener.Addr()), token); err != nil {
			return err
		}
	}
	return app.Serve(ctx, backend, listener, handler)
}

// localOrigin is the bound address as an origin the operator can open on this
// machine; a wildcard address becomes localhost.
func localOrigin(addr net.Addr) string {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		return "http://" + addr.String()
	}
	host := tcp.IP.String()
	if tcp.IP == nil || tcp.IP.IsUnspecified() {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(tcp.Port))
}
