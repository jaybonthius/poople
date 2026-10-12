package identity_test

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strconv"
	"strings"
	"testing"

	"poople/web/services/identity"
)

// gopher-doc.1bpp.lossless.webp from golang.org/x/image testdata: 75×100.
const webpFixture = "UklGRrIBAABXRUJQVlA4TKUBAAAvSsAYAA8w//M///MfeJAkbXvaSG7m8Q3GfYSBJekwQztm/IcZlgwnmWImn2BK7aFmBtnVir6q//8VOkFE/xm4baTIu8c48ArEo6+B3zFKYln3pqClSCKX0begFTAXFOLXHSyF8cCNcZEG4OywuA4KVVfJCiArU7GAgJI8+lJP/OKMT/fBAjevg1cYB7YVkFuWga2lyPi5I0HFy5YTpWIHg0RZpkniRVW9odHAKOwosWuOGdxIyn2OvaCDvhg/we6TwadPBPbqBV58MsLmMJ8yZnOWk8SRz4N+QoyPL+MnamzMvcE1rHNEr91F9GKZPVUcS9w7PhhH36suB9qPeYb/oLk6cuTiJ0wOK3m5h1cKjW6EVZCYMK7dxcKCBdgP9HkKr9gkAO2P8GKZGWVdIAatQa+1IDpt6qyorVwdy01xdW8Jkfk6xjEXmVQQ+HQdFr6OKhIN34dXWq0+0qr6EJSCeeVLH9+gvGTLyqM65PQ44ihzlTXxQKjKbAvshXgir7Lil9w4L2bvMycmjQcqXaMCO6BlY28i+FOLzbfI1vEqxAhotocAAA=="

// banded is 300×100: red outer thirds and a blue centre third, so a centre crop is all blue.
func banded() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 300, 100))
	for x := range 300 {
		c := color.RGBA{220, 0, 0, 255}
		if x >= 100 && x < 200 {
			c = color.RGBA{0, 0, 220, 255}
		}
		for y := range 100 {
			img.Set(x, y, c)
		}
	}
	return img
}

func encoded(t *testing.T, encode func(*bytes.Buffer, image.Image) error) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := encode(&buf, banded()); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// pngHeader claims width×height with a valid IHDR and no pixel data.
func pngHeader(width, height uint32) []byte {
	var buf bytes.Buffer
	buf.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], width)
	binary.BigEndian.PutUint32(ihdr[4:], height)
	ihdr[8], ihdr[9] = 8, 6 // 8-bit RGBA
	chunk := append([]byte("IHDR"), ihdr...)
	binary.Write(&buf, binary.BigEndian, uint32(len(ihdr)))
	buf.Write(chunk)
	binary.Write(&buf, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return buf.Bytes()
}

// pngChunk appends one PNG chunk with its CRC.
func pngChunk(buf *bytes.Buffer, kind string, data []byte) {
	chunk := append([]byte(kind), data...)
	binary.Write(buf, binary.BigEndian, uint32(len(data)))
	buf.Write(chunk)
	binary.Write(buf, binary.BigEndian, crc32.ChecksumIEEE(chunk))
}

// blankPNG16 is a complete, valid all-zero 16-bit RGBA PNG, written row by row
// so the test never holds the decoded image: a small file that decodes to
// width×height×8 bytes.
func blankPNG16(t *testing.T, width, height int) []byte {
	t.Helper()
	var pixels bytes.Buffer
	z := zlib.NewWriter(&pixels)
	row := make([]byte, 1+width*8) // filter byte, then zero pixels
	for range height {
		if _, err := z.Write(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	buf.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], uint32(width))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(height))
	ihdr[8], ihdr[9] = 16, 6 // 16-bit RGBA
	pngChunk(&buf, "IHDR", ihdr)
	pngChunk(&buf, "IDAT", pixels.Bytes())
	pngChunk(&buf, "IEND", nil)
	return buf.Bytes()
}

func TestSetAvatarCropsResizesAndServesByHash(t *testing.T) {
	t.Parallel()
	db := openStore(t, t.TempDir())
	users := openIdentity(t, db, nil)
	admin, session := createAdmin(t, users)
	webp, err := base64.StdEncoding.DecodeString(webpFixture)
	if err != nil {
		t.Fatal(err)
	}
	uploads := map[string][]byte{
		"png":  encoded(t, func(b *bytes.Buffer, m image.Image) error { return png.Encode(b, m) }),
		"jpeg": encoded(t, func(b *bytes.Buffer, m image.Image) error { return jpeg.Encode(b, m, &jpeg.Options{Quality: 95}) }),
		"gif":  encoded(t, func(b *bytes.Buffer, m image.Image) error { return gif.Encode(b, m, nil) }),
		"webp": webp,
	}
	for _, format := range []string{"png", "jpeg", "gif", "webp"} {
		hash, err := users.SetAvatar(t.Context(), admin.ID, bytes.NewReader(uploads[format]))
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		picture, err := users.Avatar(t.Context(), admin.ID, hash)
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		decoded, err := png.Decode(bytes.NewReader(picture))
		if err != nil {
			t.Fatalf("%s stored as non-PNG: %v", format, err)
		}
		if b := decoded.Bounds(); b.Dx() != identity.AvatarSize || b.Dy() != identity.AvatarSize {
			t.Fatalf("%s size=%v", format, b)
		}
		if format != "webp" {
			// The crop kept only the blue centre band, edge to edge.
			for _, x := range []int{2, 128, 253} {
				r, _, b, _ := decoded.At(x, 128).RGBA()
				if r>>8 > 40 || b>>8 < 180 {
					t.Fatalf("%s pixel at x=%d is not the centre band: r=%d b=%d", format, x, r>>8, b>>8)
				}
			}
		}
		if v := viewer(t, users, session.Token); v.Account.AvatarHash != hash || v.Account.AvatarPath() != "/avatars/"+strconv.FormatInt(admin.ID, 10)+"/"+hash+".png" {
			t.Fatalf("viewer avatar=%q", v.Account.AvatarHash)
		}
	}
	current := viewer(t, users, session.Token).Account.AvatarHash
	if _, err := users.Avatar(t.Context(), admin.ID, strings.Repeat("0", 64)); !errors.Is(err, identity.ErrAvatarNotFound) {
		t.Fatal("stale hash served")
	}
	if _, err := users.Avatar(t.Context(), admin.ID, "../x"); !errors.Is(err, identity.ErrAvatarNotFound) {
		t.Fatal("malformed hash served")
	}
	if err := users.RemoveAvatar(t.Context(), admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := users.Avatar(t.Context(), admin.ID, current); !errors.Is(err, identity.ErrAvatarNotFound) {
		t.Fatal("removed picture served")
	}
	if v := viewer(t, users, session.Token); v.Account.AvatarHash != "" || v.Account.AvatarPath() != "" {
		t.Fatal("removed picture still on the account")
	}
	if _, err := users.SetAvatar(t.Context(), 999, bytes.NewReader(uploads["png"])); !errors.Is(err, identity.ErrAccountNotFound) {
		t.Fatalf("picture for a missing account: %v", err)
	}
}

func TestSetAvatarRejectsOversizedAndUnsupportedUploads(t *testing.T) {
	t.Parallel()
	db := openStore(t, t.TempDir())
	users := openIdentity(t, db, nil)
	admin, _ := createAdmin(t, users)
	oversized := append(encoded(t, func(b *bytes.Buffer, m image.Image) error { return png.Encode(b, m) }), make([]byte, identity.AvatarMaxBytes)...)
	if _, err := users.SetAvatar(t.Context(), admin.ID, bytes.NewReader(oversized)); !errors.Is(err, identity.ErrAvatarTooLarge) {
		t.Fatalf("oversized upload: %v", err)
	}
	bmp := append([]byte("BM"), make([]byte, 64)...)
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"/>`)
	for name, upload := range map[string][]byte{
		"bmp": bmp, "svg": svg, "empty": nil, "truncated png": pngHeader(64, 64),
	} {
		if _, err := users.SetAvatar(t.Context(), admin.ID, bytes.NewReader(upload)); !errors.Is(err, identity.ErrAvatarInvalid) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// 3000×3000 at 16 bits per channel is a small file that decodes to 72 MB.
	bomb := blankPNG16(t, 3000, 3000)
	if len(bomb) > identity.AvatarMaxBytes {
		t.Fatalf("bomb is %d bytes, over the upload limit", len(bomb))
	}
	for name, upload := range map[string][]byte{"claimed dimensions": pngHeader(50000, 50000), "16-bit bomb": bomb} {
		if _, err := users.SetAvatar(t.Context(), admin.ID, bytes.NewReader(upload)); !errors.Is(err, identity.ErrAvatarDimensions) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if scalar(t, db, "SELECT count(*) FROM accounts WHERE avatar IS NOT NULL") != 0 {
		t.Fatal("rejected upload stored")
	}
}
