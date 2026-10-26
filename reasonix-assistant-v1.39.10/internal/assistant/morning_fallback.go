package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"reasonix/internal/heartbeat"
)

// Fallback never calls a provider, invents mail decisions, or bypasses admission.
// Its result uses the normal delivery/acknowledgement path and stable job ID.
func (s MorningService) Fallback(ctx context.Context, t heartbeat.Task, raw string) (string, error) {
	now := s.now().In(mustShanghai())
	allowed, err := (HeartbeatService{Home: s.Home}).Allows(ctx, t, now)
	if err != nil {
		return "", err
	}
	if !allowed {
		return "", heartbeat.ErrDeferred
	}
	snap, err := s.Snapshot(ctx, t.ID)
	if errors.Is(err, sql.ErrNoRows) {
		snap = MorningSnapshot{ID: t.ID, Date: now.Format("2006-01-02"), CreatedAt: now}
	} else if err != nil {
		return "", err
	}
	if snap.Date != now.Format("2006-01-02") {
		return "", errors.New("morning fallback snapshot has wrong civil date")
	}
	if err = s.refreshFacts(ctx, &snap); err != nil {
		return "", err
	}
	var draft MorningOutput
	parsed := json.Unmarshal([]byte(raw), &draft) == nil
	out := MorningOutput{Weather: "今天的天气还没整理好", MailDecisions: []MailDecision{}, News: []MorningNews{}}
	var weatherRef map[string]any
	if parsed && validateMorningWeather(draft.Weather) == nil && snap.WeatherConfigured && NewWeatherService(s.Home).VerifyWeatherRead(ctx, t.ID, t.RunStartedAt) == nil {
		if ref, e := s.weatherReference(ctx, t); e == nil {
			out.Weather, weatherRef = draft.Weather, ref
		}
	}
	if parsed && validateMorningNews(snap, draft.News) == nil {
		out.News = draft.News
	} else {
		snap.News.Items = nil
		snap.News.Status = "unavailable"
	}
	// No failed/unchecked mail summary or action candidate enters the fallback.
	mailPaused := snap.MailStatus == "paused"
	snap.MailStatus = "unavailable"
	lines := strings.Split(renderMorning(snap, out), "\n")
	if weatherRef == nil {
		lines[1] = "今天的天气还没整理好。"
	}
	lines[3] = "邮件还没整理好，暂时没有可靠的摘要。"
	if mailPaused {
		snap.MailStatus = "paused"
		lines[3] = "邮件自动检查还暂停着。"
	}
	if len(out.News) == 0 && snap.News.Status == "unavailable" {
		lines[5] = "AI新闻还没整理好，稍后可以再看。"
	}
	message := strings.Join(lines, "\n")
	err = NewRepository(s.Home).transact(ctx, func(tx *sql.Tx) error {
		if e := releaseFallbackMail(ctx, tx, t.ID); e != nil {
			return e
		}
		return saveEntity(ctx, tx, "morning_result", courseReadID(t.ID, t.RunStartedAt), map[string]any{
			"snapshot": snap, "output": out, "message": message, "fallback": true,
			"displayed_mail_ids": []string{}, "weather_reference": weatherRef,
		})
	})
	return message, err
}

func releaseFallbackMail(ctx context.Context, tx *sql.Tx, job string) error {
	records, err := loadEntities[mailRecord](ctx, tx, "mail_message")
	if err != nil {
		return err
	}
	for _, r := range records {
		if r.JobID == job {
			r.JobID = ""
			if err = saveEntity(ctx, tx, "mail_message", r.Message.ID, r); err != nil {
				return err
			}
		}
	}
	// Finalize may have saved tentative decisions before a later storage error.
	// They must not become successful silent mail or unasked proposals when this
	// fallback is delivered. Actual user-confirmed tasks are never touched.
	_, err = tx.ExecContext(ctx, "DELETE FROM assistant_entities WHERE (kind='mail_decision' AND json_extract(payload,'$.job_id')=?) OR (kind='task_proposal' AND json_extract(payload,'$.analysis_job_id')=? AND json_extract(payload,'$.state')='pending')", job, job)
	return err
}
