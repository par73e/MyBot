package assistant

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strconv"
)

// Successful tool reads are host-recorded per execution attempt. A fluent final
// answer without actual reads must not acknowledge the queue or trigger a push.
func (s MailService) recordMailRead(ctx context.Context, id string) error {
	if os.Getenv("REASONIX_ASSISTANT_MAIL_BACKGROUND") != "1" {
		return nil
	}
	job := os.Getenv("REASONIX_ASSISTANT_JOB_ID")
	attempt := os.Getenv("REASONIX_ASSISTANT_MAIL_ATTEMPT")
	if job == "" || attempt == "" {
		return errors.New("mail background identity missing")
	}
	return s.Repository.transact(ctx, func(tx *sql.Tx) error {
		return saveEntity(ctx, tx, "mail_read_receipt", mailHash(job+"\x00"+attempt+"\x00"+id), true)
	})
}
func (s MailService) VerifyMailReads(ctx context.Context, job string, attempt int64, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.Repository.transact(ctx, func(tx *sql.Tx) error {
		for _, id := range ids {
			var value string
			key := mailHash(job + "\x00" + strconv.FormatInt(attempt, 10) + "\x00" + id)
			if err := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='mail_read_receipt' AND id=?", key).Scan(&value); err != nil || value != "true" {
				return errors.New("mail analysis did not successfully read every supplied message; retained for retry")
			}
		}
		return nil
	})
}
