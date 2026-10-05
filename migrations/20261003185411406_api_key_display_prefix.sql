-- +goose Up
-- The prefix a customer sees when a key is minted (the first 16 characters
-- of the plaintext, e.g. fp_live_f8470c7e). Only the key's SHA-256 is
-- stored, so `keys list` showed a hash-derived value that never matched the
-- prefix printed at creation, and a customer could not tell which listed key
-- a stored secret belongs to. NULL for keys minted before this column; those
-- keep the hash-derived identifier.
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS display_prefix text
    CONSTRAINT api_keys_display_prefix_chk CHECK (display_prefix IS NULL OR display_prefix ~ '^fp_[a-z]+_[0-9a-f]{8}$');

-- +goose Down
ALTER TABLE api_keys DROP COLUMN IF EXISTS display_prefix;
