package platform_test

import (
	"context"
	"testing"

	"github.com/colnio/data-pipelines/internal/platform"
	"github.com/colnio/data-pipelines/internal/testsupport"
)

func TestPgIdempotencyStore_Lookup_Miss(t *testing.T) {
	pool := testsupport.NewPool(t)
	store := platform.NewIdempotencyStore(pool)

	_, _, found, err := store.Lookup(context.Background(), "principal-a", "missing-key")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if found {
		t.Fatal("expected found=false for missing key")
	}
}

func TestPgIdempotencyStore_Lookup_Hit(t *testing.T) {
	pool := testsupport.NewPool(t)
	ctx := context.Background()
	store := platform.NewIdempotencyStore(pool)

	const principal = "principal-b"
	const key = "hit-key"
	body := []byte(`{"ok":true}`)
	if err := store.Save(ctx, principal, key, 201, body); err != nil {
		t.Fatalf("Save: %v", err)
	}

	status, gotBody, found, err := store.Lookup(ctx, principal, key)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if status != 201 {
		t.Errorf("status: got %d want 201", status)
	}
	if string(gotBody) != string(body) {
		t.Errorf("body: got %q want %q", gotBody, body)
	}
}
