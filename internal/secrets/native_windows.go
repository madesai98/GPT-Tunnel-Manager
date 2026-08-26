//go:build windows

package secrets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

type nativeStore struct {
	dir string
}

func newNative(root string) Store {
	return &nativeStore{dir: filepath.Join(root, "data", "secrets")}
}

func (s *nativeStore) path(ref string) string {
	h := sha256.Sum256([]byte(ref))
	return filepath.Join(s.dir, hex.EncodeToString(h[:])+".bin")
}

func (s *nativeStore) Put(ctx context.Context, ref string, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}

	input := blobFromBytes(value)
	var output windows.DataBlob
	if err := windows.CryptProtectData(
		&input,
		nil,
		nil,
		0,
		nil,
		windows.CRYPTPROTECT_UI_FORBIDDEN,
		&output,
	); err != nil {
		return err
	}
	defer freeBlob(&output)

	protected := append([]byte(nil), unsafe.Slice(output.Data, output.Size)...)
	return os.WriteFile(s.path(ref), protected, 0o600)
}

func (s *nativeStore) Get(ctx context.Context, ref string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	protected, err := os.ReadFile(s.path(ref))
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	input := blobFromBytes(protected)
	var output windows.DataBlob
	if err := windows.CryptUnprotectData(
		&input,
		nil,
		nil,
		0,
		nil,
		windows.CRYPTPROTECT_UI_FORBIDDEN,
		&output,
	); err != nil {
		return nil, err
	}
	defer freeBlob(&output)

	return append([]byte(nil), unsafe.Slice(output.Data, output.Size)...), nil
}

func (s *nativeStore) Delete(ctx context.Context, ref string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := os.Remove(s.path(ref))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func blobFromBytes(value []byte) windows.DataBlob {
	blob := windows.DataBlob{Size: uint32(len(value))}
	if len(value) != 0 {
		blob.Data = &value[0]
	}
	return blob
}

func freeBlob(blob *windows.DataBlob) {
	if blob == nil || blob.Data == nil {
		return
	}
	_, _ = windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(blob.Data))))
	blob.Data = nil
	blob.Size = 0
}
