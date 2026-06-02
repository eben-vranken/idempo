package pg_test

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eben-vranken/idempo"
	"github.com/eben-vranken/idempo/pg"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestClaimNewKey(t *testing.T) {
	store, _ := newTestStore(t, time.Hour*24, time.Minute*5)

	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"

	result, _ := store.Claim(context.Background(), key, requestHash, "token")

	if result.Status != idempo.StatusNew {
		t.Errorf("Status returned = %s, requested %s", result.Status, idempo.StatusNew)
	}
}

func TestClaimReturnsPending(t *testing.T) {
	store, _ := newTestStore(t, time.Hour*24, time.Minute*5)

	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"

	_, _ = store.Claim(context.Background(), key, requestHash, "token")

	result, _ := store.Claim(context.Background(), key, requestHash, "token")

	if result.Status != idempo.StatusPending {
		t.Errorf("Status returned = %s, requested %s", result.Status, idempo.StatusPending)
	}
}

func TestClaimCompletedKey(t *testing.T) {
	store, _ := newTestStore(t, time.Hour*24, time.Minute*5)

	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"

	_, _ = store.Claim(context.Background(), key, requestHash, "token")

	wantBody := []byte(`{"ok": "true"}`)
	headerBytes := []byte(`{"Content-Type":["text/plain"]}`)

	err := store.Complete(context.Background(), key, "token", 201, headerBytes, wantBody)

	if err != nil {
		t.Fatal(err)
	}

	result, _ := store.Claim(context.Background(), key, requestHash, "token")

	if result.Status != idempo.StatusCompleted {
		t.Errorf("Status returned = %s, requested %s", result.Status, idempo.StatusCompleted)
	}

	if result.Code != 201 {
		t.Errorf("Status code returned = %d, requested %d", result.Code, 201)
	}

	if !bytes.Equal(result.Body, wantBody) {
		t.Errorf("Response body returned = %s, requested %s", result.Body, wantBody)
	}

	if !bytes.Equal(result.Headers, headerBytes) {
		t.Errorf("Header returned = %s, requested %s", result.Headers, headerBytes)
	}
}

func TestClaimConflictedKey(t *testing.T) {
	store, _ := newTestStore(t, time.Hour*24, time.Minute*5)

	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"

	result, _ := store.Claim(context.Background(), key, requestHash, "token")

	err := store.Complete(context.Background(), key, "token", result.Code, result.Headers, result.Body)

	if err != nil {
		t.Fatal(err)
	}

	differentRequestHash := "81fd8e12b33d548c41873494bb73c2c6b841b157ce0860857e70f41af9f24337"
	result, _ = store.Claim(context.Background(), key, differentRequestHash, "token")

	if result.Status != idempo.StatusConflict {
		t.Errorf("Status returned = %s, requested %s", result.Status, idempo.StatusConflict)
	}
}

func TestExpiredTTL(t *testing.T) {
	store, _ := newTestStore(t, time.Millisecond, time.Millisecond)

	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"

	_, _ = store.Claim(context.Background(), key, requestHash, "token")

	time.Sleep(10 * time.Millisecond)

	result, _ := store.Claim(context.Background(), key, requestHash, "token")

	if result.Status != idempo.StatusNew {
		t.Errorf("Status returned = %s, requested %s", result.Status, idempo.StatusNew)
	}
}

func TestSweepDeletesExpired(t *testing.T) {
	// Long lock so the claim doesn't expire before Complete; tiny retention
	// so the completed row is sweepable almost immediately.
	store, connString := newTestStore(t, time.Minute, time.Millisecond)

	ctx := context.Background()
	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"

	_, _ = store.Claim(ctx, key, requestHash, "token")

	if err := store.Complete(ctx, key, "token", 200, []byte("{}"), []byte("body")); err != nil {
		t.Fatal(err)
	}

	// Let the completed row's retention window lapse.
	time.Sleep(10 * time.Millisecond)

	if err := store.Sweep(ctx); err != nil {
		t.Fatalf("Sweep returned error: %v", err)
	}

	// Verify the row was actually deleted, not merely lazily reset by a Claim.
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM pgStore WHERE idempoKey = $1", key).Scan(&count); err != nil {
		t.Fatal(err)
	}

	if count != 0 {
		t.Errorf("Row count after Sweep = %d, expected 0", count)
	}
}

func newTestStore(t *testing.T, lockTTL time.Duration, retentionTTL time.Duration) (*pg.PostgresStore, string) {
	ctx := context.Background()
	container, err := postgres.Run(ctx, "postgres:18", postgres.BasicWaitStrategies())

	if err != nil {
		t.Fatal(err)
	}

	connString, err := container.ConnectionString(ctx, "sslmode=disable")

	if err != nil {
		t.Fatal(err)
	}

	pgs, err := pg.New(connString, lockTTL, retentionTTL)

	if err != nil {
		t.Fatal(err)
	}

	err = pg.RunMigration(connString)

	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		pgs.Close()
		container.Terminate(ctx)
	})
	return pgs, connString
}

func TestClaimConcurrentSingleWinner(t *testing.T) {
	store, _ := newTestStore(t, time.Hour*24, time.Minute*5)

	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"
	var newCount atomic.Int32
	const N = 50
	statuses := make([]idempo.ClaimStatus, N)
	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := range N {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			token := fmt.Sprintf("token-%d", i)
			<-start
			result, err := store.Claim(context.Background(), key, requestHash, token)

			if err != nil {
				t.Errorf("DB error: %s", err)
			}

			statuses[i] = result.Status

			if result.Status == idempo.StatusNew {
				newCount.Add(1)
			}
		}(i)
	}

	close(start)
	wg.Wait()

	if newCount.Load() != 1 {
		t.Errorf("Atomic count returned = %d, expected %d", newCount.Load(), 1)
	}

	for _, c := range statuses {
		if c != idempo.StatusNew && c != idempo.StatusPending {
			t.Errorf("Status returned = %s, expected 'new' or 'pending'", c)
		}
	}
}
