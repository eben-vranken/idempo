package pg

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/eben-vranken/idempo"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var _ idempo.Store = (*PostgresStore)(nil)

type PostgresStore struct {
	pool         *pgxpool.Pool
	lockTTL      time.Duration
	retentionTTL time.Duration
	closeOnce    sync.Once
	done         chan struct{}
}

func (ims *PostgresStore) Close() {
	ims.closeOnce.Do(func() {
		close(ims.done)
	})
}

func (pgs *PostgresStore) Sweep(ctx context.Context) error {
	_, err := pgs.pool.Exec(ctx, "DELETE FROM pgStore WHERE expiryTime < now()")
	if err != nil {
		return fmt.Errorf("pg sweep: %w", err)
	}
	return nil
}

type Entry struct {
	state           string
	bodyHash        *string
	responseCode    *int
	responseHeaders []byte
	responseBody    []byte
	expiryTime      time.Time
}

func (pgs *PostgresStore) Claim(ctx context.Context, key string, bodyHash string, token string) (idempo.ClaimResult, error) {
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
	SET state = 'pending', token = EXCLUDED.token, bodyHash = EXCLUDED.bodyHash, responseCode = NULL, responseHeaders = NULL, responseBody = NULL, expiryTime = EXCLUDED.expiryTime
	WHERE pgStore.expiryTime < now() RETURNING state`, key, token, bodyHash, time.Now().Add(pgs.lockTTL))

	err := row.Scan(&state)

	if err == pgx.ErrNoRows {
		var entry Entry
		row := pgs.pool.QueryRow(ctx, `SELECT 
		state, bodyHash, responseCode, responseHeaders, responseBody, expiryTime 
		FROM pgStore WHERE idempoKey = $1`, key)

		err := row.Scan(&entry.state, &entry.bodyHash, &entry.responseCode, &entry.responseHeaders, &entry.responseBody, &entry.expiryTime)

		if err != nil {
			return idempo.ClaimResult{}, fmt.Errorf("pg claim select: %w", err)
		} else if entry.state == string(idempo.StatusPending) {
			return idempo.ClaimResult{Status: idempo.StatusPending}, nil
		} else if entry.state == string(idempo.StatusCompleted) && entry.bodyHash != nil && bodyHash == *entry.bodyHash {
			return idempo.ClaimResult{Status: idempo.StatusCompleted, Code: *entry.responseCode, Headers: entry.responseHeaders, Body: entry.responseBody}, nil
		} else {
			return idempo.ClaimResult{Status: idempo.StatusConflict}, nil
		}
	}

	if err != nil {
		return idempo.ClaimResult{}, fmt.Errorf("pg claim upsert: %w", err)
	}

	return idempo.ClaimResult{Status: idempo.StatusNew}, nil
}

func (pgs *PostgresStore) Complete(ctx context.Context, key string, token string, statusCode int, headers []byte, body []byte) error {
	_, err := pgs.pool.Exec(ctx, `
				UPDATE pgStore
				SET
					state = $2,
					responseCode = $3,
					responseHeaders = $4,
					responseBody = $5,
					expiryTime = $7
				WHERE idempoKey = $1 AND token = $6 AND state = 'pending'
			`, key, string(idempo.StatusCompleted), statusCode, headers, body, token, time.Now().Add(pgs.retentionTTL))

	if err != nil {
		return fmt.Errorf("pg complete: %w", err)
	}
	return nil
}

func (pgs *PostgresStore) Abandon(ctx context.Context, key string, token string) error {
	_, err := pgs.pool.Exec(ctx, `
				DELETE FROM pgStore
				WHERE idempoKey = $1 AND token = $2 AND state = 'pending'
			`, key, token)

	if err != nil {
		return fmt.Errorf("pg abandon: %w", err)
	}
	return nil
}

func New(connStr string, lockTTL time.Duration, retentionTTL time.Duration) (*PostgresStore, error) {
	pgs := new(PostgresStore)
	pool, err := pgxpool.New(context.Background(), connStr)
	if err != nil {
		return nil, err
	}
	pgs.pool = pool
	pgs.lockTTL = lockTTL
	pgs.retentionTTL = retentionTTL

	pgs.done = make(chan struct{})
	ticker := time.NewTicker(5 * time.Minute)
	go func() {
		for {
			select {
			case <-ticker.C:
				pgs.Sweep(context.Background())
			case <-pgs.done:
				ticker.Stop()
				return
			}
		}
	}()

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
		responseHeaders BYTEA NULL,
		responseBody BYTEA NULL,
		expiryTime TIMESTAMPTZ NOT NULL
	);
	
	CREATE INDEX IF NOT EXISTS idx_pgstore_expiry ON pgStore (expiryTime);
	`)

	return err
}
