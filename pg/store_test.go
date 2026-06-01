package pg_test

import (
	"context"
	"testing"

	"github.com/eben-vranken/idempo/pg"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestClaimNewKey(t *testing.T) {
	store := newTestStore(t)

	key := "019e7514-3f4e-7e25-b2cf-9d33b76340eb"
	requestHash := "5d1aae56cb6a81850e92f3fdd528cf06f7f95eb13fb485ac73ebd5fbc30b1c8f"

	status, _, _, _ := store.Claim(context.Background(), key, requestHash)

	if status != "new" {
		t.Errorf("Status returned = %s, requested %s", status, "new")
	}
}

func newTestStore(t *testing.T) *pg.PostgresStore {
	ctx := context.Background()
	container, err := postgres.Run(ctx, "postgres:18", postgres.BasicWaitStrategies())

	if err != nil {
		t.Fatal(err)
	}

	connString, err := container.ConnectionString(ctx, "sslmode=disable")

	if err != nil {
		t.Fatal(err)
	}

	pgs, err := pg.New(connString)

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
