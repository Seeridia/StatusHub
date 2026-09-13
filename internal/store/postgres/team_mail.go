package postgres

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"time"
)

// Claim and finish use independent transactions; SMTP is never called under a DB lock.
func (s *Store) RunIdentityMail(ctx context.Context, send func(context.Context, string) error) error {
	tx, e := s.db.BeginTx(ctx, pgx.TxOptions{})
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `UPDATE identity_mail_jobs SET payload=NULL,lease_until=NULL WHERE payload IS NOT NULL AND (expires_at<=now() OR completed_at IS NOT NULL OR attempts>=6)`); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `UPDATE identity_mutations SET secret_result=NULL WHERE secret_until<=now() AND secret_result IS NOT NULL`); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `DELETE FROM identity_rate_limits WHERE window_start<now()-interval '1 day'`); e != nil {
		return e
	}
	var id, payload string
	lease := uuid.NewString()
	e = tx.QueryRow(ctx, `UPDATE identity_mail_jobs SET lease_token=$1,lease_until=now()+interval '60 seconds',attempts=attempts+1 WHERE id=(SELECT id FROM identity_mail_jobs WHERE payload IS NOT NULL AND expires_at>now() AND completed_at IS NULL AND attempts<6 AND next_attempt_at<=now() AND (lease_until IS NULL OR lease_until<now()) ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id,payload`, lease).Scan(&id, &payload)
	if e == pgx.ErrNoRows {
		return tx.Commit(ctx)
	}
	if e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	sendCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	sendErr := send(sendCtx, payload)
	_, e = s.db.Exec(ctx, `UPDATE identity_mail_jobs SET completed_at=CASE WHEN $3 THEN now() ELSE NULL END,payload=CASE WHEN $3 OR attempts>=6 THEN NULL ELSE payload END,lease_until=NULL,next_attempt_at=now()+make_interval(secs=>least(900,power(2,attempts)::integer*15)) WHERE id=$1 AND lease_token=$2 AND lease_until>now()`, id, lease, sendErr == nil)
	if e != nil {
		return e
	}
	return sendErr
}
