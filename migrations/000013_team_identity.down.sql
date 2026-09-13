BEGIN;
DROP TABLE identity_mutations,identity_rate_limits,identity_mail_jobs,identity_tokens,team_invitations,browser_sessions,local_credentials;
ALTER TABLE tenant_memberships DROP COLUMN enabled;
COMMIT;
