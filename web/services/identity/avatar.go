package identity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
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
	// avatarMaxPixels bounds decoded memory; a 2 MiB file can claim huge dimensions.
	avatarMaxPixels = 40_000_000
)

var (
	ErrAvatarTooLarge = errors.New("picture must be 2 MB or smaller")
	ErrAvatarInvalid  = errors.New("picture must be a PNG, JPEG, GIF or WebP image")
	ErrAvatarNotFound = errors.New("picture not found")
)

var avatarFormats = map[string]bool{"png": true, "jpeg": true, "gif": true, "webp": true}

// SetAvatar decodes an uploaded picture, centre-crops it, resizes it to 256 px,
// re-encodes it as PNG and stores it with its content hash. Callers authorize
// accountID. It returns the new hash.
func (s *Service) SetAvatar(ctx context.Context, accountID int64, upload io.Reader) (string, error) {
	encoded, err := avatarPNG(upload)
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
	return hash, nil
}

func avatarPNG(upload io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(upload, AvatarMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read picture: %w", err)
	}
	if len(data) > AvatarMaxBytes {
		return nil, ErrAvatarTooLarge
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || !avatarFormats[format] || config.Width <= 0 || config.Height <= 0 || config.Width*config.Height > avatarMaxPixels {
		return nil, ErrAvatarInvalid
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
