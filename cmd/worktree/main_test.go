package main

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupPreservesAddressAndPrivateFiles(t *testing.T) {
	root := t.TempDir()
	private := map[string]string{
		".env":               "fixture credentials\n",
		".dev/fixture.json":  "fixture dev state\n",
		"data/poople.sqlite": "fixture data\n",
	}
	for path, content := range private {
		path = filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	address, err := setupAddress(root)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	// Repeated setup must not change the address of an already running server.
	again, err := setupAddress(root)
	if err != nil || again != address {
		t.Fatalf("repeated setup: %q, %v", again, err)
	}
	other, err := setupAddress(t.TempDir())
	if err != nil || other == address {
		t.Fatalf("another worktree reused the occupied port: %q, %v", other, err)
	}
	for path, content := range private {
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil || string(data) != content {
			t.Fatalf("setup changed %s", path)
		}
	}
}

func TestSetupRejectsInvalidConfigurationWithoutReplacingIt(t *testing.T) {
	for _, content := range []string{
		"", "LISTEN=127.0.0.1:0\n", "LISTEN=0.0.0.0:8080\n",
		"LISTEN=127.0.0.1:70000\n", "LISTEN=127.0.0.1:8080\nDATA_DIR=./data\n",
	} {
		root := t.TempDir()
		path := filepath.Join(root, ".dev", "worktree.env")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := setupAddress(root); err == nil {
			t.Fatalf("accepted invalid configuration %q", content)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != content {
			t.Fatal("invalid configuration was replaced")
		}
	}
}

func TestOccupiedAddressIsNotReassignedOrReleased(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	address := listener.Addr().String()
	if err := checkAddress(address); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("occupied address: %v", err)
	}
	if second, err := net.Listen("tcp", address); err == nil {
		second.Close()
		t.Fatal("check released another process's listener")
	}
	for _, address := range []string{"127.0.0.1:0", "127.0.0.1:", "127.0.0.1:70000"} {
		if err := checkAddress(address); err == nil {
			t.Fatalf("accepted non-fixed address %q", address)
		}
	}
}

func TestLiveUsesWorktreeAddressAndRejectsCollisionBeforeSetup(t *testing.T) {
	root := t.TempDir()
	copyFile := func(from, to string) {
		t.Helper()
		data, err := os.ReadFile(from)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, to)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	copyFile("../../Taskfile.yml", "Taskfile.yml")
	copyFile("main.go", "cmd/worktree/main.go")
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n\ngo 1.26.1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	invalid := exec.CommandContext(t.Context(), "go", "run", "./cmd/worktree", "--check-listen=")
	invalid.Dir = root
	if output, err := invalid.CombinedOutput(); err == nil || !strings.Contains(string(output), "fixed host:port") {
		t.Fatalf("empty check address accepted: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(root, ".dev")); !os.IsNotExist(err) {
		t.Fatal("empty check address created configuration")
	}
	address, err := setupAddress(root)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	// Task should prefer worktree configuration to .env, while explicit LISTEN wins.
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("LISTEN=127.0.0.1:1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runTask := func(args ...string) (string, error) {
		t.Helper()
		command := exec.CommandContext(t.Context(), "task", args...)
		command.Dir = root
		for _, item := range os.Environ() {
			if !strings.HasPrefix(item, "LISTEN=") && !strings.HasPrefix(item, "DATA_DIR=") {
				command.Env = append(command.Env, item)
			}
		}
		output, err := command.CombinedOutput()
		return string(output), err
	}
	output, err := runTask("live", "DATA_DIR=./not-created")
	if err == nil || !strings.Contains(output, address+" is unavailable") {
		t.Fatalf("live did not reject the worktree port collision: %v\n%s", err, output)
	}
	for _, path := range []string{".tools", ".live", "not-created"} {
		if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
			t.Fatalf("failed preflight created %s", path)
		}
	}
	output, err = runTask("--dry", "live", "DATA_DIR=./not-created", "LISTEN=127.0.0.1:45678")
	if err != nil || !strings.Contains(output, "--check-listen 127.0.0.1:45678") {
		t.Fatalf("explicit address override: %v\n%s", err, output)
	}
}
