CREATE TABLE IF NOT EXISTS pgStore (
    idempoKey VARCHAR(255) NOT NULL PRIMARY KEY,
    state VARCHAR(20) NOT NULL,
    token VARCHAR(255) NOT NULL,
    bodyHash VARCHAR(255) NULL,
    responseCode INT NULL,
    responseHeaders BYTEA NULL,
    responseBody BYTEA NULL,
    expiryTime TIMESTAMPTZ NOT NULL,
);

CREATE INDEX IF NOT EXISTS idx_pgstore_expiry ON pgStore (expiryTime);