package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	check := flag.String("check-listen", "", "check a fixed development address without starting a server")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected arguments")
		os.Exit(1)
	}
	var err error
	if flag.NFlag() != 0 {
		err = checkAddress(*check)
	} else {
		var address string
		address, err = setupAddress(".")
		if err == nil {
			fmt.Printf("Development URL: http://%s (saved in .dev/worktree.env)\n", address)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func setupAddress(root string) (string, error) {
	path := filepath.Join(root, ".dev", "worktree.env")
	data, err := os.ReadFile(path)
	if err == nil {
		address, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "LISTEN=")
		host, _, splitErr := net.SplitHostPort(address)
		if !ok || splitErr != nil || host != "127.0.0.1" || fixedPort(address) != nil {
			return "", fmt.Errorf("invalid %s; correct it or remove only that file and rerun task worktree:setup", path)
		}
		return address, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer listener.Close()
	address := listener.Addr().String()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	_, writeErr := fmt.Fprintf(file, "LISTEN=%s\n", address)
	return address, errors.Join(writeErr, file.Close())
}

func fixedPort(address string) error {
	_, port, err := net.SplitHostPort(address)
	if err == nil {
		var number uint64
		number, err = strconv.ParseUint(port, 10, 16)
		if err == nil && number == 0 {
			err = errors.New("port must not be zero")
		}
	}
	if err != nil {
		return fmt.Errorf("development needs a fixed host:port address, not %q: %w", address, err)
	}
	return nil
}

func checkAddress(address string) error {
	if err := fixedPort(address); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("development address %s is unavailable; leave other servers running and choose an explicit LISTEN=host:port: %w", address, err)
	}
	if err := listener.Close(); err != nil {
		return err
	}
	fmt.Printf("Development URL: http://%s\n", address)
	return nil
}
