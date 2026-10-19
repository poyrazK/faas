-- Users GoTrue created before config.mjs set GOTRUE_JWT_DEFAULT_GROUP_NAME
-- have an empty role, which GoTrue copies into every token's role claim
-- (ADR-1190). A role someone set through the admin API stays.
UPDATE auth.users SET role = 'authenticated' WHERE role IS NULL OR role = '';
