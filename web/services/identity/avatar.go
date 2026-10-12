package identity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"

	"poople/store/zz"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
	"zombiezen.com/go/sqlite"
)

const (
	// AvatarMaxBytes bounds an uploaded picture before decoding.
	AvatarMaxBytes = 2 << 20
	// AvatarSize is the side of the stored square PNG.
	AvatarSize = 256
	// avatarMaxDecodedBytes bounds the memory one decode may take; a 2 MiB file
	// can claim huge dimensions. It allows 16 megapixels at 4 bytes a pixel.
	avatarMaxDecodedBytes = 64 << 20
)

// avatarDecodes serialises decoding so concurrent uploads cannot multiply its memory.
var avatarDecodes = make(chan struct{}, 1)

var (
	ErrAvatarTooLarge = errors.New("picture must be 2 MB or smaller")
	// ErrAvatarDimensions refuses a picture whose decoded pixels exceed avatarMaxDecodedBytes.
	ErrAvatarDimensions = errors.New("picture has too many pixels")
	ErrAvatarInvalid    = errors.New("picture must be a PNG, JPEG, GIF or WebP image")
	ErrAvatarNotFound   = errors.New("picture not found")
)

var avatarFormats = map[string]bool{"png": true, "jpeg": true, "gif": true, "webp": true}

// SetAvatar decodes an uploaded picture, centre-crops it, resizes it to 256 px,
// re-encodes it as PNG and stores it with its content hash. Callers authorize
// accountID. It returns the new hash.
func (s *Service) SetAvatar(ctx context.Context, accountID int64, upload io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(upload, AvatarMaxBytes+1))
	if err != nil {
		return "", fmt.Errorf("read picture: %w", err)
	}
	if len(data) > AvatarMaxBytes {
		return "", ErrAvatarTooLarge
	}
	select {
	case avatarDecodes <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	encoded, err := avatarPNG(data)
	<-avatarDecodes
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	hash := hex.EncodeToString(sum[:])
	if err := s.db.Write(ctx, func(conn *sqlite.Conn) error {
		if err := zz.OnceSetAccountAvatar(conn, zz.SetAccountAvatarParams{Avatar: &encoded, AvatarHash: &hash, Id: accountID}); err != nil {
			return err
		}
		if conn.Changes() != 1 {
			return ErrAccountNotFound
		}
		return nil
	}); err != nil {
		if errors.Is(err, ErrAccountNotFound) {
			return "", err
		}
		return "", fmt.Errorf("store picture: %w", err)
	}
	s.notifyAccounts()
	return hash, nil
}

func avatarPNG(data []byte) ([]byte, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || !avatarFormats[format] || config.Width <= 0 || config.Height <= 0 {
		return nil, ErrAvatarInvalid
	}
	if avatarDecodedBytes(config, format, data) > avatarMaxDecodedBytes {
		return nil, ErrAvatarDimensions
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrAvatarInvalid
	}
	bounds := src.Bounds()
	side := min(bounds.Dx(), bounds.Dy())
	if side <= 0 {
		return nil, ErrAvatarInvalid
	}
	x := bounds.Min.X + (bounds.Dx()-side)/2
	y := bounds.Min.Y + (bounds.Dy()-side)/2
	dst := image.NewRGBA(image.Rect(0, 0, AvatarSize, AvatarSize))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, image.Rect(x, y, x+side, y+side), draw.Src, nil)
	var out bytes.Buffer
	if err := png.Encode(&out, dst); err != nil {
		return nil, fmt.Errorf("encode picture: %w", err)
	}
	return out.Bytes(), nil
}

// avatarDecodedBytes estimates the memory decoding takes from the image's
// dimensions and the colour model its decoder returns, rounding up.
func avatarDecodedBytes(config image.Config, format string, data []byte) int64 {
	perPixel, components := int64(8), int64(4) // unknown models: 16-bit RGBA
	switch config.ColorModel {
	case color.GrayModel, color.AlphaModel:
		perPixel, components = 1, 1
	case color.Gray16Model, color.Alpha16Model:
		perPixel, components = 2, 1
	case color.YCbCrModel: // counted without chroma subsampling
		perPixel, components = 3, 3
	case color.RGBAModel, color.NRGBAModel, color.CMYKModel, color.NYCbCrAModel:
		perPixel = 4
	default:
		if _, ok := config.ColorModel.(color.Palette); ok {
			perPixel, components = 1, 1
		}
	}
	// A progressive JPEG (SOF2 marker) also keeps every coefficient as an
	// int32 per component. The marker can appear in metadata too, which only
	// tightens the bound.
	if format == "jpeg" && bytes.Contains(data, []byte{0xFF, 0xC2}) {
		perPixel += 4 * components
	}
	return int64(config.Width) * int64(config.Height) * perPixel
}

// RemoveAvatar deletes the account's picture. Callers authorize accountID.
func (s *Service) RemoveAvatar(ctx context.Context, accountID int64) error {
	if err := s.db.Write(ctx, func(conn *sqlite.Conn) error {
		if err := zz.OnceRemoveAccountAvatar(conn, accountID); err != nil {
			return err
		}
		if conn.Changes() != 1 {
			return ErrAccountNotFound
		}
		return nil
	}); err != nil {
		if errors.Is(err, ErrAccountNotFound) {
			return err
		}
		return fmt.Errorf("remove picture: %w", err)
	}
	s.notifyAccounts()
	return nil
}

// Avatar returns the stored PNG when hash is the account's current picture hash.
func (s *Service) Avatar(ctx context.Context, accountID int64, hash string) ([]byte, error) {
	if len(hash) != sha256.Size*2 {
		return nil, ErrAvatarNotFound
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return nil, ErrAvatarNotFound
	}
	var picture []byte
	if err := s.db.Read(ctx, func(conn *sqlite.Conn) (err error) {
		picture, err = zz.OnceGetAccountAvatar(conn, zz.GetAccountAvatarParams{Id: accountID, AvatarHash: &hash})
		return err
	}); err != nil {
		return nil, fmt.Errorf("load picture: %w", err)
	}
	if len(picture) == 0 {
		return nil, ErrAvatarNotFound
	}
	return picture, nil
}
