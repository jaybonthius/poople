package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"poople/store"
)

func TestLauncherRejectsInvalidInputBeforeStorage(t *testing.T) {
	for _, args := range [][]string{
		{}, {"serve"}, {"--unknown"}, {"--listen="}, {"--data-dir", " "},
		{"--data-dir", "PLACEHOLDER", "run"}, {"--data-dir", "PLACEHOLDER", "--listen="},
		{"--data-dir", "PLACEHOLDER", "--secure-cookie=maybe"},
		{"--data-dir", "PLACEHOLDER", "--ui-fps=0"},
		{"--data-dir", "PLACEHOLDER", "--ui-fps=-1"},
		{"--data-dir", "PLACEHOLDER", "--ui-fps=1000000001"},
		{"--data-dir", "PLACEHOLDER", "--ui-fps=fast"},
		{"--data-dir", "PLACEHOLDER", "--trusted-proxy=maybe"},
	} {
		dir := filepath.Join(t.TempDir(), "not-created")
		for i := range args {
			if args[i] == "PLACEHOLDER" {
				args[i] = dir
			}
		}
		if err := run(t.Context(), args, io.Discard); err == nil {
			t.Fatalf("invalid launcher arguments accepted: %v", args)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("invalid arguments created storage")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	dir := filepath.Join(t.TempDir(), "not-created")
	if err := run(ctx, []string{"--data-dir", dir}, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled startup: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("cancelled startup created storage")
	}
	if err := run(t.Context(), []string{"--help"}, io.Discard); err != nil {
		t.Fatal(err)
	}
}

type unavailableOutput struct{}

func (unavailableOutput) Write([]byte) (int, error) {
	return 0, errors.New("operator output unavailable")
}

func TestLauncherStartupFailuresReleaseResources(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	dir := filepath.Join(t.TempDir(), "not-created")
	if err := run(t.Context(), []string{"--data-dir", dir, "--listen", addr}, io.Discard); err == nil {
		t.Fatal("occupied listen address accepted")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("failed bind created storage")
	}
	listener.Close()
	if err := run(t.Context(), []string{"--data-dir", dir, "--listen", addr}, unavailableOutput{}); err == nil {
		t.Fatal("operator output failure ignored")
	}
	listener, err = net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("startup leaked listener: %v", err)
	}
	listener.Close()
	db, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatalf("failed startup left storage unusable: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWebBinaryServesAndExitsOnSIGTERM(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "poople")
	build := exec.CommandContext(t.Context(), "go", "build", "-trimpath", "-o", binary, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("CGO-free binary build: %v\n%s", err, output)
	}
	dir := t.TempDir()
	cmd := exec.CommandContext(t.Context(), binary, "--data-dir", dir, "--listen", "127.0.0.1:0")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	ready := make(chan string, 1)
	setupLink := ""
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			if link, ok := strings.CutPrefix(scanner.Text(), "Admin setup (15 minutes): "); ok {
				setupLink = link
			}
			if addr, ok := strings.CutPrefix(scanner.Text(), "Listening on "); ok {
				ready <- addr
				io.Copy(io.Discard, stderr)
				return
			}
		}
		ready <- ""
	}()
	var addr string
	select {
	case addr = <-ready:
		if addr == "" {
			t.Fatal("binary exited before listening")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("binary failed to become ready")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	if !strings.HasPrefix(setupLink, "/setup?token=") {
		t.Fatalf("empty database printed no admin setup link: %q", setupLink)
	}
	setup, err := client.Get("http://" + addr + setupLink)
	if err != nil {
		t.Fatal(err)
	}
	setup.Body.Close()
	if setup.StatusCode != 200 {
		t.Fatalf("printed setup link status=%d", setup.StatusCode)
	}
	resp, err := client.Get("http://" + addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), `<main id="main" class="play"`) {
		t.Fatalf("real binary page status=%d", resp.StatusCode)
	}
	// An open live stream must not keep the process from exiting.
	stream, err := client.Get("http://" + addr + "/play/state")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	select {
	case err := <-wait:
		if err != nil {
			t.Fatalf("SIGTERM did not exit cleanly: %v", err)
		}
	case <-time.After(15 * time.Second):
		cmd.Process.Kill()
		<-wait
		t.Fatal("SIGTERM did not join application")
	}
	if _, err := os.Stat(filepath.Join(dir, store.Filename)); err != nil {
		t.Fatal(err)
	}
}
