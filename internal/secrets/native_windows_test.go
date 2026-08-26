//go:build windows

package secrets

import (
	"bytes"
	"context"
	"testing"
)

func TestWindowsDPAPIRoundTrip(t *testing.T) {
	store := New(t.TempDir())
	ctx := context.Background()
	ref := "secret://test/dpapi"
	want := []byte("secret-value-123")

	if err := store.Put(ctx, ref, want); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := store.Get(ctx, ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("round trip mismatch: got %q want %q", got, want)
	}
	if err := store.Delete(ctx, ref); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(ctx, ref); err != ErrNotFound {
		t.Fatalf("Get after delete: got %v want ErrNotFound", err)
	}
}
