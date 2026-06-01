package inmem_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/eben-vranken/idempo/inmem"
)

func TestClaimNewKey(t *testing.T) {
	ctx := context.Background()
	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"

	store := inmem.New(24 * time.Hour)

	status, _, _, _, err := store.Claim(ctx, key, requestHash, "token")

	if err != nil {
		t.Errorf("Error returned = %s, requested nil", err)
	}

	if status != "new" {
		t.Errorf("Status returned = %s, requested %s", status, "new")
	}
}

func TestCompleteUnclaimedKey(t *testing.T) {
	ctx := context.Background()
	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"

	store := inmem.New(24 * time.Hour)

	err := store.Complete(ctx, key, "token", 200, []byte(""), []byte(""))

	if err == nil {
		t.Errorf("Error returned = nil, expected %q", "key was not found")
	}
}

func TestClaimReturnsPending(t *testing.T) {
	ctx := context.Background()
	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"
	statusExpected := "pending"
	ttl := 24 * time.Hour

	store := inmem.New(ttl)

	_, _, _, _, _  = store.Claim(ctx, key, requestHash, "token")

	status, _, _, _ , err := store.Claim(ctx, key, requestHash, "token")

	if err != nil {
		t.Errorf("Error returned = %s, requested nil", err)
	}

	if status != statusExpected {
		t.Errorf("Status returned = %s, requested %s", status, statusExpected)
	}
}

func TestReturnsCompleted(t *testing.T) {
	ctx := context.Background()
	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"
	statusExpected := "completed"
	ttl := 24 * time.Hour

	store := inmem.New(ttl)
	status, _, _, _, _ := store.Claim(ctx, key, requestHash, "token")
	
	headerBytes := []byte(`{"Content-Type":["text/plain"]}`)
	err := store.Complete(ctx, key, "token", 200, headerBytes, []byte(""))
	status, _, savedHeaders, _, _  := store.Claim(ctx, key, requestHash, "token")

	if err != nil {
		t.Errorf("Error returned = %s, requested nil", err)
	}

	if status != statusExpected {
		t.Errorf("Status returned = %s, requested %s", status, statusExpected)
	}

	if !bytes.Equal(savedHeaders, headerBytes) {
		t.Errorf("Header returned = %s, requested %s", savedHeaders, headerBytes)
	}
}

func TestClaimExpiredKeyIsNew(t *testing.T) {
	ctx := context.Background()
	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"
	statusExpected := "new"
	ttl := -1 * time.Hour

	store := inmem.New(ttl)

	_, _, _, _ , _ = store.Claim(ctx, key, requestHash, "token")
	status, _, _, _ , err := store.Claim(ctx, key, requestHash, "token")

	if err != nil {
		t.Errorf("Error returned = %s, requested nil", err)
	}

	if status != statusExpected {
		t.Errorf("Status returned = %s, requested %s", status, statusExpected)
	}
}
