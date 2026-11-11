-- filename: 20261110135754109_object_multipart_uncompleted_bucket_idx.sql
-- migration-compat: expand
-- ObjectUsageBuckets sums each bucket's part grants over its uploads with
-- state <> 'completed'. No index served that predicate (the partial ones
-- leave out 'aborted', and aborted rows are kept), so every object usage
-- read scanned the whole multipart table once per bucket of the account,
-- under the account lock of every PUT and part. Built concurrently so
-- multipart writes never wait.
-- +goose NO TRANSACTION
-- +goose Up
SET statement_timeout = 0;
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS object_storage_multipart_uncompleted_bucket_idx;
CREATE INDEX CONCURRENTLY object_storage_multipart_uncompleted_bucket_idx
 ON object_storage_multipart_uploads (bucket_id, id) WHERE state <> 'completed';

RESET statement_timeout;

-- +goose Down
SET statement_timeout = 0;
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS object_storage_multipart_uncompleted_bucket_idx;
RESET statement_timeout;
