-- +goose Up
CREATE TABLE usage_scripts (
    id              BIGSERIAL PRIMARY KEY,
    base_url_host   TEXT NOT NULL,
    account_type    VARCHAR(20) NOT NULL,
    script          TEXT NOT NULL,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ
);

CREATE UNIQUE INDEX idx_usage_scripts_host_type ON usage_scripts (base_url_host, account_type) WHERE deleted_at IS NULL;
