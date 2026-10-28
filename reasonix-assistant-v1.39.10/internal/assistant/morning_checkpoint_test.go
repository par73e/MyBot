package assistant

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"reasonix/internal/heartbeat"
)

func checkMorningDraft(t *testing.T, s MorningService, out MorningOutput) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"action": "check", "draft": out})
	if err != nil {
		t.Fatal(err)
	}
	got, err := (MorningTool{Service: s}).Execute(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestMorningCheckedDraftSurvivesInterruptedFinalAndInvalidCorrection(t *testing.T) {
	hs, s, job, out := readyMorningDraft(t)
	if got := checkMorningDraft(t, s, out); !strings.Contains(got, `"valid":true`) {
		t.Fatal(got)
	}
	// A later failed correction must not replace the last fully checked draft.
	out.MailDecisions[0].Summary = strings.Repeat("长", 1001)
	if got := checkMorningDraft(t, s, out); !strings.Contains(got, `"valid":false`) {
		t.Fatal(got)
	}
	if err := hs.repo().transact(t.Context(), func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(t.Context(), "SELECT count(*) FROM assistant_entities WHERE kind IN ('mail_decision','task_proposal','morning_result','morning_delivered')").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Fatal("checking a draft published decisions or delivery", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	message, err := s.RecoverCheckedDraft(t.Context(), job)
	if err != nil || !strings.Contains(message, "Twitch开播了") || strings.Contains(message, "普通广告") || strings.Contains(message, "还没整理好") {
		t.Fatal(message, err)
	}
	// Delivery acknowledgement remains the caller's responsibility.
	latest, err := (MorningTool{Service: s}).Execute(t.Context(), json.RawMessage(`{"action":"latest"}`))
	if err != nil || latest != `{"available":false}` {
		t.Fatal("recovery pretended delivery had occurred", latest, err)
	}
}

func TestMorningCheckedDraftRevalidatesReadsAndCurrentAdmission(t *testing.T) {
	for _, change := range []string{"mail_read", "disabled", "attempt", "date", "finished", "expired"} {
		t.Run(change, func(t *testing.T) {
			hs, s, job, out := readyMorningDraft(t)
			if got := checkMorningDraft(t, s, out); !strings.Contains(got, `"valid":true`) {
				t.Fatal(got)
			}
			if err := hs.repo().transact(t.Context(), func(tx *sql.Tx) error {
				switch change {
				case "mail_read":
					_, err := tx.ExecContext(t.Context(), "DELETE FROM assistant_entities WHERE kind='mail_read_receipt'")
					return err
				case "disabled":
					cfg, err := loadHeartbeatConfig(t.Context(), tx)
					if err != nil {
						return err
					}
					cfg.ModuleEnabled["morning_briefing"] = false
					return saveEntity(t.Context(), tx, "heartbeat_config", heartbeatConfigID, cfg)
				case "attempt", "finished":
					stored, err := loadMorningJob(t.Context(), tx, job.ID)
					if err != nil {
						return err
					}
					if change == "attempt" {
						stored.History[len(stored.History)-1].StartedAt++
					} else {
						stored.History[len(stored.History)-1].Status = "delivered"
					}
					return saveEntity(t.Context(), tx, "job", job.ID, stored)
				case "date":
					var raw string
					if err := tx.QueryRowContext(t.Context(), "SELECT payload FROM assistant_entities WHERE kind='morning_checked_draft'").Scan(&raw); err != nil {
						return err
					}
					var saved morningCheckpoint
					if err := json.Unmarshal([]byte(raw), &saved); err != nil {
						return err
					}
					saved.Date = "2026-10-26"
					return saveEntity(t.Context(), tx, "morning_checked_draft", courseReadID(job.ID, job.RunStartedAt), saved)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if change == "expired" {
				s.Clock = func() time.Time { return timeMust("2026-10-27T08:05:00+08:00") }
			}
			if got, err := s.RecoverCheckedDraft(t.Context(), job); err == nil || got != "" {
				t.Fatal("recovery bypassed current evidence/admission", got, err)
			}
		})
	}
}

func TestMorningLegacyAndInvalidChecksDoNotCreateRecoveryDraft(t *testing.T) {
	_, s, job, out := readyMorningDraft(t)
	if _, err := (MorningTool{Service: s}).Execute(t.Context(), json.RawMessage(`{"action":"check","news":[]}`)); err != nil {
		t.Fatal(err)
	}
	out.MailDecisions[0].Summary = strings.Repeat("长", 1001)
	checkMorningDraft(t, s, out)
	if _, err := s.RecoverCheckedDraft(t.Context(), job); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("unchecked draft became recoverable", err)
	}
	other := job
	other.RunStartedAt++
	if _, err := s.RecoverCheckedDraft(t.Context(), other); !errors.Is(err, heartbeat.ErrDeferred) {
		t.Fatal("foreign attempt was admitted", err)
	}
}
