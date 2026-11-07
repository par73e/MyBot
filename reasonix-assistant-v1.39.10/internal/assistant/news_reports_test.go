package assistant

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
)

func completedNewsFixture(t *testing.T, s HotNewsService, snap HotNewsSnapshot) []provider.Message {
	t.Helper()
	var messages []provider.Message
	call := func(id string, args any) {
		raw, _ := json.Marshal(args)
		result, err := (NewsTool{Service: s}).Execute(t.Context(), raw)
		if err != nil {
			t.Fatal(err)
		}
		messages = append(messages, provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: id, Name: "assistant_ai_news", Arguments: string(raw)}}}, provider.Message{Role: provider.RoleTool, ToolCallID: id, Content: result, ToolRunState: provider.ToolRunCompleted})
	}
	events := []NewsEventSummary{{IDs: []string{snap.Candidates[0].ID}, Title: "新模型发布", Summary: "团队公布了模型实测结果。"}, {IDs: []string{snap.Candidates[1].ID}, Title: "研究进展", Summary: "研究团队更新推理实验。"}}
	call("read", map[string]any{"action": "read", "snapshot_id": snap.ID, "ids": []string{snap.Candidates[0].ID, snap.Candidates[1].ID}})
	call("check", map[string]any{"action": "check", "events": events})
	messages = append(messages, provider.Message{ID: "final", Role: provider.RoleAssistant, Content: renderNewsBriefing(snap, NewsBriefingOutput{Events: events}), CreatedAt: s.now().UnixMilli()})
	return messages
}
func TestCompletedNewsAcrossSourcesUsesGenerationTime(t *testing.T) {
	s, _, snap := hotFixture(t)
	messages := completedNewsFixture(t, s, snap)
	if err := s.RecordCompletedNewsTurn(t.Context(), "weixin", "friday-owner", messages); err != nil {
		t.Fatal(err)
	}
	// A delayed web response generated earlier must not hide the newer WeChat report.
	older := append([]provider.Message(nil), messages...)
	older[len(older)-1].CreatedAt -= 60000
	if err := s.RecordCompletedNewsTurn(t.Context(), "web", "web-owner", older); err != nil {
		t.Fatal(err)
	}
	raw, err := s.latest(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var latest struct {
		Source      string          `json:"source"`
		GeneratedAt time.Time       `json:"generated_at"`
		Message     string          `json:"message"`
		Snapshot    HotNewsSnapshot `json:"snapshot"`
	}
	if err = json.Unmarshal([]byte(raw), &latest); err != nil {
		t.Fatal(err)
	}
	if latest.Source != "weixin" || latest.Message != messages[len(messages)-1].Content || latest.Snapshot.ID != snap.ID || !latest.GeneratedAt.Equal(s.now()) {
		t.Fatal(raw)
	}
	newer := append([]provider.Message(nil), messages...)
	newer[len(newer)-1].CreatedAt += 60000
	newer[len(newer)-1].ID = "new-final"
	if err = s.RecordCompletedNewsTurn(t.Context(), "web", "web-owner", newer); err != nil {
		t.Fatal(err)
	}
	raw, err = s.latest(t.Context())
	if err != nil || !strings.Contains(raw, `"source":"web"`) {
		t.Fatal(raw, err)
	}
	if err = s.RecordCompletedNewsTurn(t.Context(), "web", "web-owner", newer); err != nil {
		t.Fatal(err)
	}
	// Retrying the same completed turn is idempotent.
	err = NewRepository(s.Home).transact(t.Context(), func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRow("SELECT count(*) FROM assistant_entities WHERE kind='ai_news_report'").Scan(&count); err != nil {
			return err
		}
		if count != 3 {
			t.Fatalf("count=%d", count)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestIncompleteOrUnbackedNewsNeverReplacesReport(t *testing.T) {
	s, _, snap := hotFixture(t)
	good := completedNewsFixture(t, s, snap)
	for _, name := range []string{"candidates", "followup", "partial", "invalid-check", "failed-read", "wrong-snapshot", "local-only", "uncited"} {
		t.Run(name, func(t *testing.T) {
			messages := append([]provider.Message(nil), good...)
			switch name {
			case "candidates":
				messages = messages[:2]
			case "followup":
				messages[len(messages)-1].Content = "第一条的详细内容"
			case "partial":
				messages[len(messages)-1].Content = strings.ReplaceAll(messages[len(messages)-1].Content, "研究团队更新推理实验。", "")
			case "invalid-check":
				messages[3].Content = `{"valid":false}`
			case "failed-read":
				messages[1].ToolRunState = provider.ToolRunFailed
			case "wrong-snapshot":
				messages[1].Content = strings.ReplaceAll(messages[1].Content, snap.ID, "other-snapshot")
			case "local-only":
				messages[len(messages)-1].LocalOnly = true
			case "uncited":
				messages[len(messages)-1].Content = strings.ReplaceAll(messages[len(messages)-1].Content, "https://example.com/", "https://unread.example/")
			}
			if err := s.RecordCompletedNewsTurn(t.Context(), "weixin", "owner", messages); err != nil {
				t.Fatal(err)
			}
			raw, err := s.latest(t.Context())
			if err != nil || raw != `{"available":false}` {
				t.Fatal(raw, err)
			}
		})
	}
}
func TestLatestNewsFallsBackToLegacyDelivery(t *testing.T) {
	s, _, _ := hotFixture(t)
	err := NewRepository(s.Home).transact(t.Context(), func(tx *sql.Tx) error {
		return saveEntity(t.Context(), tx, "ai_news_delivered", "old", map[string]any{"message": "historical report"})
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := s.latest(t.Context())
	if err != nil || !strings.Contains(raw, "historical report") {
		t.Fatal(raw, err)
	}
}

func TestLatestIncludesConfirmedMorningNewsWithoutPrivateMorningData(t *testing.T) {
	service, _, snap := hotFixture(t)
	at := service.now().Add(time.Hour)
	page := (MorningService{Home: service.Home, News: &service, Clock: service.Clock}).prepareHotNews(t.Context())
	out := MorningOutput{News: []MorningNews{}}
	for _, item := range page.Items {
		out.News = append(out.News, MorningNews{ID: item.ID, Summary: "官方发布模型更新"})
	}
	value := map[string]any{"snapshot": MorningSnapshot{CreatedAt: at, News: page, MailIDs: []string{"PRIVATE_MAIL_REFERENCE"}}, "output": out, "message": "PRIVATE_MORNING_MAIL_WEATHER_AND_TASKS"}
	save := func(kind, id string, v any) {
		t.Helper()
		if err := NewRepository(service.Home).transact(t.Context(), func(tx *sql.Tx) error { return saveEntity(t.Context(), tx, kind, id, v) }); err != nil {
			t.Fatal(err)
		}
	}
	save("ai_news_delivered", "old", map[string]any{"snapshot": snap, "message": "old standalone report"})
	save("morning_result", "pending", value)
	raw, err := service.latest(t.Context())
	if err != nil || strings.Contains(raw, `"source":"morning"`) {
		t.Fatal("unacknowledged morning leaked", raw, err)
	}
	save("morning_delivered", "confirmed", value)
	raw, err = service.latest(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Source      string             `json:"source"`
		GeneratedAt time.Time          `json:"generated_at"`
		Snapshot    HotNewsSnapshot    `json:"snapshot"`
		Output      NewsBriefingOutput `json:"output"`
	}
	if err = json.Unmarshal([]byte(raw), &report); err != nil {
		t.Fatal(err)
	}
	if report.Source != "morning" || !report.GeneratedAt.Equal(at) || len(report.Output.Events) != 3 || report.Snapshot.ID != snap.ID || strings.Contains(raw, "PRIVATE_") || !strings.Contains(raw, page.Items[0].URL) {
		t.Fatal(raw)
	}
	// A newly generated manual report takes precedence over the morning edition.
	messages := completedNewsFixture(t, service, snap)
	messages[len(messages)-1].CreatedAt = at.Add(time.Hour).UnixMilli()
	if err = service.RecordCompletedNewsTurn(t.Context(), "web", "web-owner", messages); err != nil {
		t.Fatal(err)
	}
	raw, err = service.latest(t.Context())
	if err != nil || !strings.Contains(raw, `"source":"web"`) {
		t.Fatal(raw, err)
	}
}
func TestMorningWithMissingNewsReferencesCannotHideAValidReport(t *testing.T) {
	service, _, _ := hotFixture(t)
	page := (MorningService{Home: service.Home, News: &service, Clock: service.Clock}).prepareHotNews(t.Context())
	output := MorningOutput{News: []MorningNews{{ID: page.Items[0].ID, Summary: "模型更新"}}}
	err := NewRepository(service.Home).transact(t.Context(), func(tx *sql.Tx) error {
		good := map[string]any{"snapshot": MorningSnapshot{CreatedAt: service.now(), News: page}, "output": output}
		if err := saveEntity(t.Context(), tx, "morning_delivered", "valid", good); err != nil {
			return err
		}
		bad := map[string]any{"snapshot": MorningSnapshot{CreatedAt: service.now().Add(time.Hour)}, "output": output}
		return saveEntity(t.Context(), tx, "morning_delivered", "missing-news", bad)
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := service.latest(t.Context())
	if err != nil || !strings.Contains(raw, `"source":"morning"`) || !strings.Contains(raw, page.Items[0].URL) {
		t.Fatal(raw, err)
	}
}
