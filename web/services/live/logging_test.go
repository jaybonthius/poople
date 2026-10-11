package live

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

func TestStreamLogsDroppedSnapshotFailureOnlyOnce(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	failure := errors.New("snapshot storage failed")
	for _, err := range []error{context.Canceled, failure} {
		output.Reset()
		got := Stream(t.Context(), httptest.NewRecorder(), httptest.NewRequest("GET", "/play/state?token=private-query", nil), 30, func() (templ.Component, <-chan struct{}, error) { return nil, nil, err })
		if !errors.Is(got, err) {
			t.Fatalf("error = %v", got)
		}
		if err != failure {
			if output.Len() != 0 {
				t.Fatal(output.String())
			}
		} else if strings.Count(output.String(), "\n") != 1 || !strings.Contains(output.String(), "snapshot storage failed") || strings.Contains(output.String(), "private-query") {
			t.Fatal(output.String())
		}
	}
}
