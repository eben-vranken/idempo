package inmem_test

import (
	"context"
	"testing"
	"time"

	"github.com/eben-vranken/idempo/inmem"
)

func TestInMemClaimValidKey(t *testing.T) {
	cases := []struct {
		name        string
		ctx         context.Context
		key         string
		requestHash string
		err         error
		status      string
		double      bool
		completed   bool
	}{
		{
			name:        "Registering new key",
			ctx:         context.Background(),
			key:         "019e7514-3f4e-7e25-b2cf-9d33b76340eb",
			requestHash: "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f",
			err:         nil,
			status:      "new",
			double:      false,
			completed:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := inmem.New(24 * time.Hour)

			var status string
			var err error

			status, _, _, err = store.Claim(tc.ctx, tc.key, tc.requestHash)

			if tc.double {
				status, _, _, err = store.Claim(tc.ctx, tc.key, tc.requestHash)
			}

			if tc.completed {
				if status != tc.status {
					t.Errorf("Status returned = %s, requested %s", status, tc.status)
				}
			} else {
				if err != nil {
					t.Errorf("Error returned = %s, requested %s", err, tc.err)
				}

				if status != tc.status {
					t.Errorf("Status returned = %s, requested %s", status, tc.status)
				}
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

	_, _, _, _ = store.Claim(ctx, key, requestHash)

	status, _, _, err := store.Claim(ctx, key, requestHash)

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

	status, _, _, _ := store.Claim(ctx, key, requestHash)
	err := store.Complete(ctx, key, 200, []byte(""))
	status, _, _, _ = store.Claim(ctx, key, requestHash)

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

	_, _, _, _ = store.Claim(ctx, key, requestHash)
	status, _, _, err := store.Claim(ctx, key, requestHash)

	if err != nil {
		t.Errorf("Error returned = %s, requested nil", err)
	}

	if status != statusExpected {
		t.Errorf("Status returned = %s, requested %s", status, statusExpected)
	}
}
