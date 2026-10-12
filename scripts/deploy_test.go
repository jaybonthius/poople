package scripts_test

import (
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Real file transfers and HTTPS; only build/service control and the SSH transport
// are replaced, so these tests cannot deploy an app or touch a real service.
func TestDeploy(t *testing.T) {
	for _, host := range []string{"local", "test-server"} {
		t.Run(host, func(t *testing.T) {
			t.Parallel()
			checkout, dest, run := fixture(t, host)
			write(t, filepath.Join(dest, "data/poople.sqlite"), "deployed database", 0600)
			write(t, filepath.Join(dest, "data/poople.sqlite-wal"), "deployed WAL", 0600)
			write(t, filepath.Join(dest, ".env"), "deployed settings", 0600)
			write(t, filepath.Join(dest, "poople"), "previous binary", 0700)
			write(t, filepath.Join(checkout, "data/dev-only"), "never deploy this", 0600)
			write(t, filepath.Join(checkout, ".env"), "development settings", 0600)
			run(true)
			assertFile(t, filepath.Join(dest, "data/poople.sqlite"), "deployed database")
			assertFile(t, filepath.Join(dest, "data/poople.sqlite-wal"), "deployed WAL")
			assertFile(t, filepath.Join(dest, ".env"), "deployed settings")
			assertFile(t, filepath.Join(dest, "poople"), "new binary")
			if _, err := os.Stat(filepath.Join(dest, "data/dev-only")); !os.IsNotExist(err) {
				t.Fatalf("unexpected development file: %v", err)
			}
			for name, want := range map[string]string{"poople.sqlite": "deployed database", "poople.sqlite-wal": "deployed WAL"} {
				backups, err := filepath.Glob(filepath.Join(dest, "backups/*/data", name))
				if err != nil || len(backups) != 1 {
					t.Fatalf("backups of %s = %v, %v", name, backups, err)
				}
				assertFile(t, backups[0], want)
			}
			info, err := os.Stat(filepath.Join(dest, "backups"))
			if err != nil || info.Mode().Perm() != 0700 {
				t.Fatalf("backup directory must be private: %v, %v", info, err)
			}
			assertFile(t, filepath.Join(checkout, "operations"), "check\nrace\nstop\nstart\n")
		})
	}
}

func TestDeployFirstInstall(t *testing.T) {
	t.Parallel()
	checkout, dest, run := fixture(t, "local")
	run(true)
	assertFile(t, filepath.Join(dest, "poople"), "new binary")
	assertFile(t, filepath.Join(checkout, "operations"), "check\nrace\nstop\nstart\n")
	if _, err := os.Stat(filepath.Join(dest, "data")); !os.IsNotExist(err) {
		t.Fatalf("deployment must leave data creation to the app: %v", err)
	}
}

func TestDeployFailures(t *testing.T) {
	for _, failure := range []string{"missing-config", "bad-host", "root-dir", "http-url", "overlap", "symlink", "service-dir", "check", "race", "missing-binary", "copy", "health"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			checkout, dest, run := fixture(t, "local")
			write(t, filepath.Join(dest, "data/private.txt"), "keep me", 0600)
			settings := func(host, dir, url string) {
				write(t, filepath.Join(checkout, ".deploy.env"), fmt.Sprintf("DEPLOY_HOST=%q\nDEPLOY_DIR=%q\nDEPLOY_URL=%q\n", host, dir, url), 0600)
			}
			switch failure {
			case "missing-config":
				if err := os.Remove(filepath.Join(checkout, ".deploy.env")); err != nil {
					t.Fatal(err)
				}
			case "bad-host":
				settings("-oProxyCommand=true", dest, "https://example.com")
			case "root-dir":
				settings("local", "/", "https://example.com")
			case "http-url":
				settings("local", dest, "http://example.com")
			case "overlap":
				settings("local", checkout, "https://example.com")
			case "service-dir":
				write(t, filepath.Join(checkout, "working-dir"), "/wrong-service-directory", 0600)
			case "symlink":
				if err := os.Symlink(filepath.Join(dest, "data"), filepath.Join(dest, "backups")); err != nil {
					t.Fatal(err)
				}
			case "missing-binary":
				if err := os.Remove(filepath.Join(checkout, "bin/poople")); err != nil {
					t.Fatal(err)
				}
			default:
				write(t, filepath.Join(checkout, "fail"), failure, 0600)
			}
			output := run(false)
			if want := map[string]string{
				"missing-config": "Copy .deploy.env.example", "bad-host": "SSH host alias", "root-dir": "not /",
				"http-url": "must use HTTPS", "overlap": "must not overlap", "symlink": "Refusing symlinked",
				"service-dir": "WorkingDirectory matching", "health": "Health check returned HTTP 302",
			}[failure]; !strings.Contains(output, want) {
				t.Fatalf("output for %s lacks %q:\n%s", failure, want, output)
			}
			assertFile(t, filepath.Join(dest, "data/private.txt"), "keep me")
			operations, err := os.ReadFile(filepath.Join(checkout, "operations"))
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if failure == "health" {
				assertFile(t, filepath.Join(checkout, "operations"), "check\nrace\nstop\nstart\n")
			} else if strings.Contains(string(operations), "start") || (failure != "copy" && strings.Contains(string(operations), "stop")) {
				t.Fatalf("unsafe service operations after %s: %s", failure, operations)
			}
			if failure == "copy" {
				assertFile(t, filepath.Join(checkout, "operations"), "check\nrace\nstop\n")
				backups, err := filepath.Glob(filepath.Join(dest, "backups/*/data/private.txt"))
				if err != nil || len(backups) != 1 {
					t.Fatalf("missing backup after failed copy: %v, %v", backups, err)
				}
				assertFile(t, backups[0], "keep me")
			}
		})
	}
}

func fixture(t *testing.T, host string) (string, string, func(bool) string) {
	t.Helper()
	root := t.TempDir()
	checkout := filepath.Join(root, "checkout")
	dest := filepath.Join(root, "server install 'quoted'")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			t.Errorf("unexpected health check: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if failure, _ := os.ReadFile(filepath.Join(checkout, "fail")); string(failure) == "health" {
			http.Redirect(w, r, "/wrong-page", http.StatusFound)
			return
		}
		fmt.Fprint(w, "Poople")
	}))
	t.Cleanup(server.Close)
	write(t, filepath.Join(root, "ca.pem"), string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})), 0600)
	script, err := os.ReadFile("deploy.sh")
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(checkout, "scripts/deploy.sh"), string(script), 0755)
	write(t, filepath.Join(checkout, "working-dir"), dest, 0600)
	write(t, filepath.Join(checkout, ".deploy.env"), fmt.Sprintf("DEPLOY_HOST=%s\nDEPLOY_DIR=%q\nDEPLOY_URL=%s\n", host, dest, server.URL), 0600)
	write(t, filepath.Join(checkout, "bin/poople"), "new binary", 0755)
	realRsync, err := exec.LookPath("rsync")
	if err != nil {
		t.Fatal("deployment tests require rsync:", err)
	}
	for name, body := range map[string]string{
		"task": `echo "$1" >>"$FIXTURE/operations"
[[ ! -f $FIXTURE/fail || $(<"$FIXTURE/fail") != "$1" ]]`,
		"sudo": `[[ $1 == -n ]] && shift
exec "$@"`,
		"systemctl": `case "$*" in
"show -p WorkingDirectory --value poople.service") cat "$FIXTURE/working-dir" ;;
"stop poople.service") echo stop >>"$FIXTURE/operations" ;;
"enable --now poople.service") echo start >>"$FIXTURE/operations" ;;
*) exit 99 ;;
esac`,
		"ssh": `[[ $1 == -o && $2 == BatchMode=yes ]] && shift 2
[[ $1 == test-server ]] || exit 99
shift
exec bash -c "$*"`,
		"rsync": `if [[ -f $FIXTURE/fail && $(<"$FIXTURE/fail") == copy && "$*" == *bin/poople* ]]; then exit 1; fi
exec "$REAL_RSYNC" "$@"`,
	} {
		write(t, filepath.Join(root, "bin", name), "#!/bin/bash\nset -euo pipefail\n"+body+"\n", 0755)
	}
	return checkout, dest, func(success bool) string {
		t.Helper()
		cmd := exec.Command("bash", filepath.Join(checkout, "scripts/deploy.sh"))
		cmd.Env = append(os.Environ(), "PATH="+filepath.Join(root, "bin")+":"+os.Getenv("PATH"), "FIXTURE="+checkout, "REAL_RSYNC="+realRsync, "CURL_CA_BUNDLE="+filepath.Join(root, "ca.pem"), "NO_PROXY=127.0.0.1", "DEPLOY_HOST=wrong-inherited-host", "DEPLOY_DIR=/wrong-inherited-dir")
		output, err := cmd.CombinedOutput()
		if (err == nil) != success {
			t.Fatalf("deployment success=%t, error=%v:\n%s", success, err, output)
		}
		return string(output)
	}
}

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("%s = %q, %v; want %q", path, got, err, want)
	}
}
