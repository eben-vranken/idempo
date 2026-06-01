package inmem_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/eben-vranken/idempo/inmem"
)

func TestInMemStoreOperations(t *testing.T) {
	cases := []struct {
		name        string
		ctx         context.Context
		key         string
		requestHash string
		wantBool    bool
		err         error
		status      string
		claimed     bool
		completed   bool
	}{
		{
			name:        "Registering new key",
			ctx:         context.Background(),
			key:         "019e7514-3f4e-7e25-b2cf-9d33b76340eb",
			requestHash: "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f",
			wantBool:    false,
			err:         nil,
			status:      "new",
			claimed:     true,
			completed:   false,
		},
		{
			name:        "Complete an unclaimed key",
			ctx:         context.Background(),
			key:         "019e7514-3f4e-7e25-b2cf-9d33b76340eb",
			requestHash: "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f",
			wantBool:    true,
			err:         errors.New("key was not found"),
			status:      "",
			claimed:     false,
			completed:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := inmem.New(24 * time.Hour)

			var status string
			var err error

			if tc.claimed {
				status, _, _, err = store.Claim(tc.ctx, tc.key, tc.requestHash, "token")
			}

			if tc.completed {
				err = store.Complete(tc.ctx, tc.key, "token", 200, []byte(""))
			}

			if status != tc.status {
				t.Errorf("Status returned = %s, requested %s", status, tc.status)
			}

			if err != nil && !tc.wantBool {
				t.Errorf("Error returned = %s, requested %s", err, tc.err)
			}
		})
	}
}

func TestClaimReturnsPending(t *testing.T) {
	ctx := context.Background()
	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"
	statusExpected := "pending"
	ttl := 24 * time.Hour

	store := inmem.New(ttl)

	_, _, _, _ = store.Claim(ctx, key, requestHash, "token")

	status, _, _, err := store.Claim(ctx, key, requestHash, "token")

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

	status, _, _, _ := store.Claim(ctx, key, requestHash, "token")
	err := store.Complete(ctx, key, "token", 200, []byte(""))
	status, _, _, _ = store.Claim(ctx, key, requestHash, "token")

	if err != nil {
		t.Errorf("Error returned = %s, requested nil", err)
	}

	if status != statusExpected {
		t.Errorf("Status returned = %s, requested %s", status, statusExpected)
	}
}

func TestClaimExpiredKeyIsNew(t *testing.T) {
	ctx := context.Background()
	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"
	statusExpected := "new"
	ttl := -1 * time.Hour

	store := inmem.New(ttl)

	_, _, _, _ = store.Claim(ctx, key, requestHash, "token")
	status, _, _, err := store.Claim(ctx, key, requestHash, "token")

	if err != nil {
		t.Errorf("Error returned = %s, requested nil", err)
	}

	if status != statusExpected {
		t.Errorf("Status returned = %s, requested %s", status, statusExpected)
	}
}
