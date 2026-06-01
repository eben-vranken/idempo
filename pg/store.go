package pg

import (
	"context"
	"time"

	"github.com/eben-vranken/idempo"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var _ idempo.Store = (*PostgresStore)(nil)

type PostgresStore struct {
	pool *pgxpool.Pool
	ttl  time.Duration
}

type Entry struct {
	state        string
	bodyHash     *string
	responseCode *int
	responseBody []byte
	expiryTime   time.Time
}

func (pgs *PostgresStore) Claim(ctx context.Context, key string, bodyHash string, token string) (string, int, []byte, error) {
	var state string
	row := pgs.pool.QueryRow(ctx, `INSERT INTO pgStore (
		idempoKey,
		state,
		token,
		bodyHash,
		expiryTime
	) VALUES (
	$1, 'pending', $2, $3, $4
	)
	ON CONFLICT (idempoKey) DO UPDATE 
	SET state = 'pending', token = EXCLUDED.token, bodyHash = EXCLUDED.bodyHash, responseCode = NULL, responseBody = NULL, expiryTime = EXCLUDED.expiryTime
	WHERE pgStore.expiryTime < now() RETURNING state`, key, token, bodyHash, time.Now().Add(pgs.ttl))

	err := row.Scan(&state)

	if err == pgx.ErrNoRows {
		var entry Entry
		row := pgs.pool.QueryRow(ctx, `SELECT 
		state, bodyHash, responseCode, responseBody, expiryTime 
		FROM pgStore WHERE idempoKey = $1`, key)

		err := row.Scan(&entry.state, &entry.bodyHash, &entry.responseCode, &entry.responseBody, &entry.expiryTime)

		if err != nil {
			return "", 0, nil, err
		} else if entry.state == "pending" {
			return "pending", 0, nil, nil
		} else if entry.state == "completed" && entry.bodyHash != nil && bodyHash == *entry.bodyHash {
			return "completed", *entry.responseCode, entry.responseBody, nil
		} else {
			return "conflict", 0, nil, nil
		}
	}

	if err != nil {
		return "", 0, nil, err
	}

	return "new", 0, nil, nil
}

func (pgs *PostgresStore) Complete(ctx context.Context, key string, token string, statusCode int, body []byte) error {
	_, err := pgs.pool.Exec(ctx, `
				UPDATE pgStore
				SET
					state = $2,
					responseCode = $3,
					responseBody = $4
				WHERE idempoKey = $1 AND token = $5 AND state = 'pending'
			`, key, "completed", statusCode, body, token)

	return err
}

func (pgs *PostgresStore) Abandon(ctx context.Context, key string, token string) error {
	_, err := pgs.pool.Exec(ctx, `
				DELETE FROM pgStore
				WHERE idempoKey = $1 AND token = $2 AND state = 'pending'
			`, key, token)

	return err
}

func New(connStr string, expireDuration time.Duration) (*PostgresStore, error) {
	pgs := new(PostgresStore)
	pool, err := pgxpool.New(context.Background(), connStr)
	if err != nil {
		return nil, err
	}

	pgs.pool = pool
	pgs.ttl = expireDuration

	return pgs, nil
}

func RunMigration(connStr string) error {
	pool, err := pgxpool.New(context.Background(), connStr)

	if err != nil {
		return err
	}

	defer pool.Close()

	_, err = pool.Exec(context.Background(), `CREATE TABLE IF NOT EXISTS pgStore (
		idempoKey VARCHAR(255) NOT NULL PRIMARY KEY,
		state VARCHAR(20) NOT NULL,
		token VARCHAR(255) NOT NULL,
		bodyHash VARCHAR(255) NULL,
		responseCode INT NULL,
		responseBody BYTEA NULL,
		expiryTime TIMESTAMPTZ NOT NULL
	);`)

	return err
}
