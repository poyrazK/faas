INSERT INTO api.team_users(subject) VALUES ('alice'),('amy'),('ann'),('bob'),('ben'),('eve');
INSERT INTO api.team_members(tenant_id,subject,role) VALUES
 ('shop-a','alice','owner'), ('shop-a','amy','admin'), ('shop-a','ann','member'),
 ('shop-b','bob','owner'), ('shop-b','ben','member');
INSERT INTO api.team_orders(id,tenant_id,created_by,total_cents,status) VALUES
 ('00000000-0000-4000-8000-000000000001','shop-a','alice',4200,'draft'),
 ('00000000-0000-4000-8000-000000000002','shop-b','bob',9000,'submitted');

-- Public, test-only expiry fixture for saved invitation scenarios. Never ship
-- seed.sql to a hosted project or reuse this known secret for real invitations.
INSERT INTO api.team_invitations(invitation_id,tenant_id,issuer,invited_role,secret_hash,expires_at)
VALUES('90000000-0000-4000-8000-000000000001','shop-a','alice','member',sha256(convert_to(repeat('0',64),'UTF8')),clock_timestamp()+interval '1 day');
