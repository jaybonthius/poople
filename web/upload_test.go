package web_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"testing"
	"time"

	"poople/app"
	"poople/web"
	"poople/web/services/identity"
)

// The server's 15 s ReadTimeout must not cut off a slow picture upload.
func TestSlowAvatarUploadOutlastsTheServerReadTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("takes over 15 seconds")
	}
	t.Parallel()
	backend, err := app.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler, setupToken, err := web.NewApp(t.Context(), backend, false, 30)
	if err != nil {
		backend.Close()
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		backend.Close()
		t.Fatal(err)
	}
	serving, stop := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- app.Serve(serving, backend, listener, handler) }() // Serve closes backend
	t.Cleanup(func() {
		stop()
		if err := <-served; err != nil {
			t.Error(err)
		}
	})
	origin := "http://" + listener.Addr().String()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	signals, _ := json.Marshal(map[string]string{"setupToken": setupToken, "username": "Admin", "password": "correct horse battery"})
	setup, _ := http.NewRequestWithContext(ctx, "POST", origin+"/setup", bytes.NewReader(signals))
	setup.Header.Set("Content-Type", "application/json")
	setup.Header.Set("Datastar-Request", "true")
	response, err := http.DefaultClient.Do(setup)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	var session *http.Cookie
	for _, cookie := range response.Cookies() {
		if cookie.Name == identity.SessionCookie {
			session = cookie
		}
	}
	if session == nil {
		t.Fatalf("setup status=%d set no session", response.StatusCode)
	}

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, _ := form.CreateFormFile("avatar", "me.png")
	if err := png.Encode(part, image.NewRGBA(image.Rect(0, 0, 64, 64))); err != nil {
		t.Fatal(err)
	}
	form.Close()
	upload := body.Bytes()
	slow, write := io.Pipe()
	go func() {
		write.Write(upload[:len(upload)/2])
		select {
		case <-time.After(16 * time.Second):
		case <-ctx.Done():
		}
		write.Write(upload[len(upload)/2:])
		write.Close()
	}()
	r, _ := http.NewRequestWithContext(ctx, "POST", origin+"/account/avatar", slow)
	r.ContentLength = int64(len(upload))
	r.Header.Set("Content-Type", form.FormDataContentType())
	r.Header.Set("Datastar-Request", "true")
	r.AddCookie(session)
	response, err = http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	reply, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("slow upload status=%d body=%s", response.StatusCode, reply)
	}
}
