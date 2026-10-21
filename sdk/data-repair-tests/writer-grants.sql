-- Disposable acceptance fixture. Substitute only a safely quoted role name.
GRANT USAGE ON SCHEMA public,gregale_repair TO __WRITER_ROLE__;
GRANT SELECT ON public.products,gregale_repair.enrollments TO __WRITER_ROLE__;
GRANT UPDATE ON public.products TO __WRITER_ROLE__;
GRANT SELECT,INSERT,UPDATE ON gregale_repair.operations,gregale_repair.changes TO __WRITER_ROLE__;
GRANT SELECT,INSERT ON public.gregale_operation_inbox,public.gregale_customer_operation_inbox TO __WRITER_ROLE__;
GRANT EXECUTE ON FUNCTION gregale_repair.begin_operation(uuid,jsonb,text,regclass,uuid),
 gregale_repair.finish_operation(uuid),gregale_repair.check_enrollment(uuid),gregale_repair.fingerprint(regclass) TO __WRITER_ROLE__;
