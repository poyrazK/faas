-- Illustrative operation identity; real handlers obtain it from Gregale ingress.
BEGIN;
SELECT gregale_repair.begin_operation(
 '33333333-3333-4333-8333-333333333333',
 '{"account_id":"11111111-1111-4111-8111-111111111111","app_id":"22222222-2222-4222-8222-222222222222","environment":"production"}',
 'buggy-import-v1','public.products');
UPDATE public.products SET category='bad-import';
SELECT gregale_repair.finish_operation('33333333-3333-4333-8333-333333333333');
COMMIT;
UPDATE public.products SET category='human-correction' WHERE id<=40;
