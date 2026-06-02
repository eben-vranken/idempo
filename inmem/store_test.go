package inmem_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/eben-vranken/idempo"
	"github.com/eben-vranken/idempo/inmem"
)

func TestClaimNewKey(t *testing.T) {
	ctx := context.Background()
	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"

	store := inmem.New(24*time.Hour, 5*time.Minute)

	result, err := store.Claim(ctx, key, requestHash, "token")

	if err != nil {
		t.Errorf("Error returned = %s, requested nil", err)
	}

	if result.Status != idempo.StatusNew {
		t.Errorf("Status returned = %s, requested %s", result.Status, idempo.StatusNew)
	}
}

func TestCompleteUnclaimedKey(t *testing.T) {
	ctx := context.Background()
	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"

	store := inmem.New(24*time.Hour, 5*time.Minute)

	err := store.Complete(ctx, key, "token", 200, []byte(""), []byte(""))

	if err == nil {
		t.Errorf("Error returned = nil, expected %q", "key was not found")
	}
}

func TestClaimReturnsPending(t *testing.T) {
	ctx := context.Background()
	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"
	statusExpected := idempo.StatusPending

	store := inmem.New(24*time.Hour, 5*time.Minute)

	_, _ = store.Claim(ctx, key, requestHash, "token")

	result, err := store.Claim(ctx, key, requestHash, "token")

	if err != nil {
		t.Errorf("Error returned = %s, requested nil", err)
	}

	if result.Status != statusExpected {
		t.Errorf("Status returned = %s, requested %s", result.Status, statusExpected)
	}
}

func TestReturnsCompleted(t *testing.T) {
	ctx := context.Background()
	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"
	statusExpected := idempo.StatusCompleted

	store := inmem.New(24*time.Hour, 5*time.Minute)
	_, _ = store.Claim(ctx, key, requestHash, "token")

	headerBytes := []byte(`{"Content-Type":["text/plain"]}`)
	err := store.Complete(ctx, key, "token", 200, headerBytes, []byte(""))
	result, _ := store.Claim(ctx, key, requestHash, "token")

	if err != nil {
		t.Errorf("Error returned = %s, requested nil", err)
	}

	if result.Status != statusExpected {
		t.Errorf("Status returned = %s, requested %s", result.Status, statusExpected)
	}

	if !bytes.Equal(result.Headers, headerBytes) {
		t.Errorf("Header returned = %s, requested %s", result.Headers, headerBytes)
	}
}

func TestClaimExpiredKeyIsNew(t *testing.T) {
	ctx := context.Background()
	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"
	statusExpected := idempo.StatusNew

	store := inmem.New(-1*time.Hour, 5*time.Minute)

	_, _ = store.Claim(ctx, key, requestHash, "token")
	result, err := store.Claim(ctx, key, requestHash, "token")

	if err != nil {
		t.Errorf("Error returned = %s, requested nil", err)
	}

	if result.Status != statusExpected {
		t.Errorf("Status returned = %s, requested %s", result.Status, statusExpected)
	}
}
