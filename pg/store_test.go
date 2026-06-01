package pg_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/eben-vranken/idempo/pg"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestClaimNewKey(t *testing.T) {
	store := newTestStore(t, time.Hour*24)

	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"

	status, _, _, _, _ := store.Claim(context.Background(), key, requestHash, "token")

	if status != "new" {
		t.Errorf("Status returned = %s, requested %s", status, "new")
	}
}

func TestClaimReturnsPending(t *testing.T) {
	store := newTestStore(t, time.Hour*24)

	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"

	_, _, _, _, _ = store.Claim(context.Background(), key, requestHash, "token")

	status, _, _, _, _ := store.Claim(context.Background(), key, requestHash, "token")

	if status != "pending" {
		t.Errorf("Status returned = %s, requested %s", status, "pending")
	}
}

func TestClaimCompletedKey(t *testing.T) {
	store := newTestStore(t, time.Hour*24)

	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"

	_, _, _, _, _ = store.Claim(context.Background(), key, requestHash, "token")

	wantBody := []byte(`{"ok": "true"}`)
	err := store.Complete(context.Background(), key, "token", 201, []byte(""), wantBody)

	if err != nil {
		t.Fatal(err)
	}

	status, statusCode, _, savedBody, _ := store.Claim(context.Background(), key, requestHash, "token")

	if status != "completed" {
		t.Errorf("Status returned = %s, requested %s", status, "completed")
	}

	if statusCode != 201 {
		t.Errorf("Status code returned = %d, requested %d", statusCode, 201)
	}

	if !bytes.Equal(savedBody, wantBody) {
		t.Errorf("Respone body returned = %s, requested %s", savedBody, wantBody)
	}
}

func TestClaimConflictedKey(t *testing.T) {
	store := newTestStore(t, time.Hour*24)

	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"

	_, statusCode, savedHeader, savedBody, _ := store.Claim(context.Background(), key, requestHash, "token")

	err := store.Complete(context.Background(), key, "token", statusCode, savedHeader, savedBody)

	if err != nil {
		t.Fatal(err)
	}

	differentRequestHash := "81fd8e12b33d548c41873494bb73c2c6b841b157ce0860857e70f41af9f24337"
	status, _, _, _, _ := store.Claim(context.Background(), key, differentRequestHash, "token")

	if status != "conflict" {
		t.Errorf("Status returned = %s, requested %s", status, "conflict")
	}
}

func TestExpiredTTL(t *testing.T) {
	store := newTestStore(t, time.Millisecond)

	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"

	_, _, _, _, _ = store.Claim(context.Background(), key, requestHash, "token")

	time.Sleep(10 * time.Millisecond)

	status, _, _, _, _ := store.Claim(context.Background(), key, requestHash, "token")

	if status != "new" {
		t.Errorf("Status returned = %s, requested %s", status, "new")
	}
}

func newTestStore(t *testing.T, expireDuration time.Duration) *pg.PostgresStore {
	ctx := context.Background()
	container, err := postgres.Run(ctx, "postgres:18", postgres.BasicWaitStrategies())

	if err != nil {
		t.Fatal(err)
	}

	connString, err := container.ConnectionString(ctx, "sslmode=disable")

	if err != nil {
		t.Fatal(err)
	}

	pgs, err := pg.New(connString, expireDuration)

	if err != nil {
		t.Fatal(err)
	}

	err = pg.RunMigration(connString)

	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { container.Terminate(ctx) })

	return pgs
}
