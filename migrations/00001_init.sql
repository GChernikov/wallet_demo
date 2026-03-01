-- +goose Up

CREATE TABLE balances
(
    wallet_uuid BINARY(16)  NOT NULL,
    balance     BIGINT      NOT NULL DEFAULT 0,
    version     BIGINT      NOT NULL DEFAULT 0,
    created_at  DATETIME(3) NOT NULL DEFAULT NOW(3),
    updated_at  DATETIME(3) NOT NULL DEFAULT NOW(3),
    PRIMARY KEY (wallet_uuid)
);

CREATE TABLE transactions
(
    id             BIGINT      NOT NULL AUTO_INCREMENT,
    transaction_id BINARY(16)  NOT NULL,
    wallet_uuid    BINARY(16)  NOT NULL,
    amount         BIGINT      NOT NULL,
    event_type     VARCHAR(64) NOT NULL,
    created_at     DATETIME(3) NOT NULL DEFAULT NOW(3),
    PRIMARY KEY (wallet_uuid, created_at, id),
    KEY (id)
)
PARTITION BY RANGE COLUMNS(created_at) (
    PARTITION p2024_12 VALUES LESS THAN ('2025-01-01 00:00:00'),
    PARTITION p2025_01 VALUES LESS THAN ('2025-02-01 00:00:00'),
    PARTITION p2025_02 VALUES LESS THAN ('2025-03-01 00:00:00'),
    PARTITION p2025_03 VALUES LESS THAN ('2025-04-01 00:00:00'),
    PARTITION p2025_04 VALUES LESS THAN ('2025-05-01 00:00:00'),
    PARTITION p2025_05 VALUES LESS THAN ('2025-06-01 00:00:00'),
    PARTITION p2025_06 VALUES LESS THAN ('2025-07-01 00:00:00'),
    PARTITION p2025_07 VALUES LESS THAN ('2025-08-01 00:00:00'),
    PARTITION p2025_08 VALUES LESS THAN ('2025-09-01 00:00:00'),
    PARTITION p2025_09 VALUES LESS THAN ('2025-10-01 00:00:00'),
    PARTITION p2025_10 VALUES LESS THAN ('2025-11-01 00:00:00'),
    PARTITION p2025_11 VALUES LESS THAN ('2025-12-01 00:00:00'),
    PARTITION p2025_12 VALUES LESS THAN ('2026-01-01 00:00:00'),
    PARTITION p2026_01 VALUES LESS THAN ('2026-02-01 00:00:00'),
    PARTITION p2026_02 VALUES LESS THAN ('2026-03-01 00:00:00'),
    PARTITION p2026_03 VALUES LESS THAN ('2026-04-01 00:00:00'),
    PARTITION p2026_04 VALUES LESS THAN ('2026-05-01 00:00:00'),
    PARTITION p_future VALUES LESS THAN (MAXVALUE)
);

CREATE TABLE event_outbox
(
    id            BIGINT       NOT NULL AUTO_INCREMENT,
    uuid          CHAR(36)     NOT NULL,
    topic         VARCHAR(255) NOT NULL,
    headers       JSON         NULL,
    options       JSON         NULL,
    payload       MEDIUMBLOB   NOT NULL,
    dispatched    TINYINT(1)   NOT NULL DEFAULT 0,
    dispatched_at INT          NULL,
    created_at    INT          NOT NULL,
    PRIMARY KEY (id),
    CONSTRAINT uq_event_outbox_uuid UNIQUE (uuid)
);

CREATE INDEX idx_event_outbox_created_at_dispatched ON event_outbox (created_at, dispatched);
CREATE INDEX idx_event_outbox_dispatched ON event_outbox (dispatched);

CREATE TABLE idempotency_keys
(
    key_hash    BINARY(16)  NOT NULL,
    wallet_uuid BINARY(16)  NOT NULL,
    result      JSON        NOT NULL,
    created_at  DATETIME(3) NOT NULL DEFAULT NOW(3),
    expires_at  DATETIME(3) NOT NULL,
    PRIMARY KEY (key_hash),
    KEY idx_idempotency_keys_expires_at (expires_at)
);

-- fixture wallet for manual testing: 00000000-0000-0000-0000-000000000001
INSERT INTO balances (wallet_uuid, balance)
VALUES (UNHEX('00000000000000000000000000000001'), 1000000);

-- +goose Down

DROP TABLE IF EXISTS idempotency_keys;
DROP TABLE IF EXISTS event_outbox;
DROP TABLE IF EXISTS transactions;
DROP TABLE IF EXISTS balances;
