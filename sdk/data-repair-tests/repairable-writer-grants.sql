-- Add to writer-grants.sql only when enabling the repairable transaction API.
GRANT SELECT,INSERT ON gregale_repair.capture_receipts TO __WRITER_ROLE__;
GRANT EXECUTE ON FUNCTION gregale_repair.capture_transaction(uuid,jsonb,text,text,text,bytea,bytea,text) TO __WRITER_ROLE__;
