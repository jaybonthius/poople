package errorlog_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"

	"poople/lib/errorlog"

	"github.com/go-chi/chi/v5"
)

func TestSafeKeepsCausesNotPrivateDiagnostics(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("create jobs/private-title: %w", &os.PathError{Op: "open", Path: "private-document", Err: os.ErrPermission}), "permission denied"},
		{&url.Error{Op: "Get", URL: "https://private-user:private-password@example.test/private-path?token=private-token", Err: context.DeadlineExceeded}, "context deadline exceeded"},
		{privateValuer{}, "bounded diagnostic"},
		{errors.New(`failed https://example.test/private-path?token=private-token sk-private-key password=private-password "private-content"`), "failed"},
		{errors.Join(errors.New("sqlite failure"), context.DeadlineExceeded), "context deadline exceeded"},
	} {
		got := errorlog.Safe(tc.err)
		if !strings.Contains(got, tc.want) || strings.Contains(got, "private-") {
			t.Fatalf("unsafe or unhelpful diagnostic: %s", got)
		}
	}
}

// privateValuer is a remote error that supplies a bounded LogValue instead of its text.
type privateValuer struct{}

func (privateValuer) Error() string        { return "private-body sk-private-key" }
func (privateValuer) LogValue() slog.Value { return slog.StringValue("bounded diagnostic") }

// Serial: default logger is restored before parallel tests are scheduled.
func TestRequestLogsDeadlineAndPatternButNotCancellation(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	router := chi.NewRouter()
	router.Get("/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		errorlog.Request(r, "read jobs", context.DeadlineExceeded)
	})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/jobs/private-path?token=private-token", nil))
	if !strings.Contains(output.String(), `"route":"/jobs/{id}"`) || !strings.Contains(output.String(), "context deadline exceeded") || strings.Contains(output.String(), "private-") {
		t.Fatal(output.String())
	}
	output.Reset()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r := httptest.NewRequest("GET", "/jobs/private-path?token=private-token", nil).WithContext(ctx)
	router.ServeHTTP(httptest.NewRecorder(), r)
	errorlog.Error(context.Background(), "normal cancellation", context.Canceled)
	errorlog.Request(httptest.NewRequest("GET", "/jobs", nil), "write response", syscall.EPIPE)
	if output.Len() != 0 {
		t.Fatal(output.String())
	}
}
