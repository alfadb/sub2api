-- Add provider field to accounts table for Aggregator platform
-- This field identifies the specific aggregator service (e.g., "openrouter", "oneapi")
-- when platform = 'aggregator'

ALTER TABLE accounts ADD COLUMN IF NOT EXISTS provider VARCHAR(50);

-- Add index for provider filtering
CREATE INDEX IF NOT EXISTS idx_accounts_provider ON accounts(provider) WHERE provider IS NOT NULL;
