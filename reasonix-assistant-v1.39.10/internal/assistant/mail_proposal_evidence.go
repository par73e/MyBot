package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"unicode/utf8"
)

func evidenceFragments(body string) []string {
	out := []string{}
	for _, p := range strings.FieldsFunc(body, func(r rune) bool { return strings.ContainsRune("\r\n。！？", r) }) {
		p = strings.TrimSpace(p)
		if utf8.RuneCountInString(p) >= 4 && utf8.RuneCountInString(p) <= 300 {
			out = append(out, p)
		}
		if len(out) >= 1000 {
			break
		}
	}
	return out
}
func (s MailService) recordProposalEvidence(ctx context.Context, id, body string) error {
	job, attempt := os.Getenv("REASONIX_ASSISTANT_JOB_ID"), os.Getenv("REASONIX_ASSISTANT_MAIL_ATTEMPT")
	if job == "" || attempt == "" || (os.Getenv("REASONIX_ASSISTANT_MAIL_BACKGROUND") != "1" && os.Getenv("REASONIX_ASSISTANT_MORNING_BACKGROUND") != "1") {
		return nil
	}
	return s.Repository.transact(ctx, func(tx *sql.Tx) error {
		key := mailHash(job + "\x00" + attempt + "\x00" + id)
		var raw string
		hashes := map[string]bool{}
		if tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='mail_proposal_evidence' AND id=?", key).Scan(&raw) == nil {
			if e := json.Unmarshal([]byte(raw), &hashes); e != nil {
				return e
			}
		}
		for _, f := range evidenceFragments(body) {
			if len(hashes) < 4000 {
				hashes[mailHash(f)] = true
			}
		}
		return saveEntity(ctx, tx, "mail_proposal_evidence", key, hashes)
	})
}
