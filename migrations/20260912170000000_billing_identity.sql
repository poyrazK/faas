-- +goose Up
-- +goose StatementBegin

-- Mutable billing identity used for the next provider invoice. Issued
-- invoices take an immutable snapshot in a follow-up migration; these
-- fields are deliberately nullable so existing accounts remain valid.
alter table accounts
  add column if not exists business_name text,
  add column if not exists billing_address text,
  add column if not exists tax_id text;

alter table accounts
  drop constraint if exists accounts_business_name_length_chk;
alter table accounts
  add constraint accounts_business_name_length_chk
  check (business_name is null or char_length(business_name) between 1 and 256);

alter table accounts
  drop constraint if exists accounts_billing_address_length_chk;
alter table accounts
  add constraint accounts_billing_address_length_chk
  check (billing_address is null or char_length(billing_address) between 1 and 1000);

alter table accounts
  drop constraint if exists accounts_tax_id_length_chk;
alter table accounts
  add constraint accounts_tax_id_length_chk
  check (tax_id is null or char_length(tax_id) between 1 and 128);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
alter table accounts drop constraint if exists accounts_tax_id_length_chk;
alter table accounts drop constraint if exists accounts_billing_address_length_chk;
alter table accounts drop constraint if exists accounts_business_name_length_chk;
alter table accounts drop column if exists tax_id;
alter table accounts drop column if exists billing_address;
alter table accounts drop column if exists business_name;
-- +goose StatementEnd
