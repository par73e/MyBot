// Built inside the deployed Reasonix module to reuse its canonical, read-only
// session decoder. This process has no writable access to Friday's source tree.
package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/joho/godotenv"
	"reasonix/internal/provider"
	"reasonix/internal/session"
	"reasonix/internal/sessioncontext"
)

const alias = "friday_owner"
const home = "/var/lib/mybot/reasonix"
const workspace = "/var/lib/mybot/workspace"
const archiveRoot = home + "/projects/-var-lib-mybot-workspace/sessions-v4"

type conversation struct {
	Date   string `json:"date"`
	Source string `json:"source"`
	ID     string `json:"-"`
}
type visibleMessage struct {
	ID        string  `json:"id"`
	Role      string  `json:"role"`
	Text      string  `json:"text"`
	Created   float64 `json:"created"`
	Source    string  `json:"source"`
	SessionID string  `json:"source_session"`
	MessageID string  `json:"source_message_id"`
	Day       string  `json:"-"`
	Ordinal   int     `json:"-"`
}
type position struct {
	Day     string
	Session string
	Ordinal int
	Scope   string
}
type historyReader interface {
	History(context.Context, session.SessionRef) ([]provider.Message, error)
}
type bridge struct {
	query                historyReader
	root                 string
	owner, legacy, token string
	redact               []string
	gate                 chan struct{}
}

func sourceIDs(chat, workspacePath string) (string, string) {
	owner := sha256.Sum256([]byte("weixin\x00" + chat + "\x00" + workspacePath))
	legacy := sha256.Sum256([]byte("weixin:dm:" + chat))
	return hex.EncodeToString(owner[:16]), "bot-" + hex.EncodeToString(legacy[:8])
}

// DailyConversation in the source SDK uses friday-<owner>-YYYYMMDD.
// Match that complete canonical identity, never arbitrary/background sessions.
// Directory metadata needs no SQLite WAL/SHM writes, even on a read-only mount.
func (b *bridge) days(ctx context.Context) ([]conversation, error) {
	entries, err := os.ReadDir(b.root)
	if err != nil {
		return nil, err
	}
	pattern := regexp.MustCompile("^friday-" + regexp.QuoteMeta(b.owner) + "-([0-9]{8})$")
	out := []conversation{}
	for _, entry := range entries {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		match := pattern.FindStringSubmatch(entry.Name())
		if !entry.IsDir() || match == nil {
			continue
		}
		day, e := time.Parse("20060102", match[1])
		if e != nil {
			continue
		}
		manifest, e := os.Lstat(filepath.Join(b.root, entry.Name(), "manifest.json"))
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return nil, e
		}
		if !manifest.Mode().IsRegular() {
			continue
		}
		out = append(out, conversation{Date: day.Format("2006-01-02"), ID: entry.Name(), Source: "friday_archive"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date > out[j].Date })
	if info, e := os.Lstat(filepath.Join(b.root, b.legacy)); e == nil && info.IsDir() {
		if manifest, e := os.Lstat(filepath.Join(b.root, b.legacy, "manifest.json")); e == nil && manifest.Mode().IsRegular() {
			out = append(out, conversation{Date: "legacy", ID: b.legacy, Source: "friday_archive"})
		} else if e != nil && !os.IsNotExist(e) {
			return nil, e
		}
	} else if e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	return out, nil
}
func visible(m provider.Message) bool {
	if (m.Role != provider.RoleUser && m.Role != provider.RoleAssistant) || m.LocalOnly || m.Origin == provider.MessageOriginHost || len(m.ToolCalls) > 0 {
		return false
	}
	if _, ok := sessioncontext.Parse(m.Content); ok && m.Origin != provider.MessageOriginUser {
		return false
	}
	return strings.TrimSpace(m.Content) != ""
}
func (b *bridge) text(content string) string {
	for _, secret := range b.redact {
		content = strings.ReplaceAll(content, secret, "[凭据已隐藏]")
	}
	return content
}
func less(a, c visibleMessage) bool {
	if a.Day != c.Day {
		return a.Day < c.Day
	}
	if a.SessionID != c.SessionID {
		return a.SessionID < c.SessionID
	}
	return a.Ordinal < c.Ordinal
}
func (b *bridge) scope(date, query string) string {
	v := sha256.Sum256([]byte(date + "\x00" + query))
	return hex.EncodeToString(v[:])
}
func (b *bridge) encode(p position) string {
	raw, _ := json.Marshal(p)
	mac := hmac.New(sha256.New, []byte(b.token))
	mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (b *bridge) decode(raw, scope string) (position, error) {
	p := position{}
	parts := strings.Split(raw, ".")
	if len(parts) != 2 || len(raw) > 2048 {
		return p, errors.New("bad cursor")
	}
	body, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		return p, e
	}
	sig, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return p, e
	}
	mac := hmac.New(sha256.New, []byte(b.token))
	mac.Write(body)
	if !hmac.Equal(sig, mac.Sum(nil)) || json.Unmarshal(body, &p) != nil || p.Scope != scope {
		return p, errors.New("bad cursor")
	}
	return p, nil
}
func (b *bridge) page(ctx context.Context, date, query, cursor string, limit int) (any, error) {
	days, err := b.days(ctx)
	if err != nil {
		return nil, err
	}
	scope := b.scope(date, query)
	var before *visibleMessage
	if cursor != "" {
		p, err := b.decode(cursor, scope)
		if err != nil {
			return nil, err
		}
		before = &visibleMessage{Day: p.Day, SessionID: p.Session, Ordinal: p.Ordinal}
	}
	all := []visibleMessage{}
	// Canonical source order, including messages whose original timestamp is
	// absent. Never fabricate timestamps or import host/tool/reasoning records.
	for _, day := range days {
		if date != "" && date != day.Date {
			continue
		}
		messages, err := b.query.History(ctx, session.SessionRef{HostID: "local", SessionID: day.ID})
		if err != nil {
			return nil, errors.New("source transcript unavailable")
		}
		for index, m := range messages {
			if !visible(m) {
				continue
			}
			text := b.text(m.Content)
			if query != "" && !strings.Contains(strings.ToLower(text), strings.ToLower(query)) {
				continue
			}
			label := day.Date
			if label == "legacy" {
				label = ""
			}
			at := float64(0)
			if m.CreatedAt > 0 {
				at = float64(m.CreatedAt) / 1000
			}
			item := visibleMessage{ID: day.ID + ":" + strconv.Itoa(index), Role: string(m.Role), Text: text, Created: at, Source: "friday_archive", SessionID: day.ID, MessageID: m.ID, Day: label, Ordinal: index}
			if before != nil && !less(item, *before) {
				continue
			}
			all = append(all, item)
		}
	}
	sort.Slice(all, func(i, j int) bool { return less(all[i], all[j]) })
	more := len(all) > limit
	if more {
		all = all[len(all)-limit:]
	}
	next := ""
	if more {
		m := all[0]
		next = b.encode(position{Day: m.Day, Session: m.SessionID, Ordinal: m.Ordinal, Scope: scope})
	}
	nextBefore := 0
	if more {
		nextBefore = all[0].Ordinal + 1
	}
	return map[string]any{"date": date, "messages": all, "has_more": more, "next_cursor": next, "next_before": nextBefore, "reference_only": true}, nil
}
func respond(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
func (b *bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+b.token)) != 1 {
		respond(w, 403, map[string]string{"error": "denied"})
		return
	}
	if r.URL.Path == "/health" {
		respond(w, 200, map[string]string{"status": "ok"})
		return
	}
	if r.URL.Query().Get("source") != alias {
		respond(w, 404, map[string]string{"error": "unknown source"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	defer cancel()
	select {
	case b.gate <- struct{}{}:
		defer func() { <-b.gate }()
	case <-ctx.Done():
		respond(w, 503, map[string]string{"error": "busy"})
		return
	}
	switch r.URL.Path {
	case "/v1/days":
		days, err := b.days(ctx)
		if err != nil {
			log.Printf("history index read failed: %v", err)
			respond(w, 503, map[string]string{"error": "source index unavailable"})
			return
		}
		respond(w, 200, map[string]any{"conversations": days})
	case "/v1/messages":
		date := r.URL.Query().Get("date")
		query := r.URL.Query().Get("query")
		cursor := r.URL.Query().Get("cursor")
		if v := r.URL.Query().Get("before"); v != "" && v != "0" && cursor == "" {
			n, e := strconv.Atoi(v)
			if e != nil || n < 1 || n > 1000000000 || date == "" {
				respond(w, 400, map[string]string{"error": "invalid before"})
				return
			}
			days, e := b.days(ctx)
			if e != nil {
				respond(w, 503, map[string]string{"error": "source index unavailable"})
				return
			}
			for _, day := range days {
				if day.Date == date {
					label := date
					if label == "legacy" {
						label = ""
					}
					cursor = b.encode(position{Day: label, Session: day.ID, Ordinal: n - 1, Scope: b.scope(date, query)})
					break
				}
			}
		}
		if date != "" && date != "legacy" {
			if _, err := time.Parse("2006-01-02", date); err != nil {
				respond(w, 400, map[string]string{"error": "invalid date"})
				return
			}
		}
		if len(query) > 400 || len(cursor) > 2048 {
			respond(w, 400, map[string]string{"error": "invalid query"})
			return
		}
		limit := 100
		if v := r.URL.Query().Get("limit"); v != "" {
			n, e := strconv.Atoi(v)
			if e != nil || n < 1 || n > 100 {
				respond(w, 400, map[string]string{"error": "invalid limit"})
				return
			}
			limit = n
		}
		if cursor != "" {
			if _, err := b.decode(cursor, b.scope(date, query)); err != nil {
				respond(w, 400, map[string]string{"error": "invalid cursor"})
				return
			}
		}
		page, err := b.page(ctx, date, query, cursor, limit)
		if err != nil {
			respond(w, 503, map[string]string{"error": "source transcript unavailable"})
			return
		}
		respond(w, 200, page)
	default:
		respond(w, 404, map[string]string{"error": "not found"})
	}
}
func main() {
	token, err := os.ReadFile("/etc/asrobot-history/token")
	if err != nil {
		log.Fatal("history token unavailable")
	}
	var cfg struct {
		Bot struct {
			Allowlist struct {
				WeixinUsers []string `toml:"weixin_users"`
			} `toml:"allowlist"`
		} `toml:"bot"`
	}
	if _, err = toml.DecodeFile(home+"/config.toml", &cfg); err != nil || len(cfg.Bot.Allowlist.WeixinUsers) != 1 {
		log.Fatal("exactly one source identity required")
	}
	path, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		log.Fatal("source workspace unavailable")
	}
	owner, legacy := sourceIDs(cfg.Bot.Allowlist.WeixinUsers[0], path)
	service, err := session.NewService("local", session.NewFilesystemPersistence(archiveRoot))
	if err != nil {
		log.Fatal("source reader unavailable")
	}
	defer service.CloseAll(context.Background())
	b := &bridge{query: service.Query(), root: archiveRoot, owner: owner, legacy: legacy, token: strings.TrimSpace(string(token)), gate: make(chan struct{}, 1)}
	if len(b.token) < 32 {
		log.Fatal("invalid history token")
	}
	// Hide current operational credentials if a user pasted them into an old
	// conversation. Neither raw tool bodies nor hidden reasoning are exposed.
	env, e := godotenv.Read(home + "/.env")
	if e == nil {
		for k, v := range env {
			k = strings.ToUpper(k)
			if len(v) >= 8 && (strings.Contains(k, "KEY") || strings.Contains(k, "TOKEN") || strings.Contains(k, "PASSWORD") || strings.Contains(k, "SECRET")) {
				b.redact = append(b.redact, v)
			}
		}
	}
	sock := "/run/asrobot-history/history.sock"
	_ = os.Remove(sock)
	listener, err := net.Listen("unix", sock)
	if err != nil {
		log.Fatal("history socket unavailable")
	}
	if err = os.Chmod(sock, 0660); err != nil {
		log.Fatal("history socket mode failed")
	}
	server := &http.Server{Handler: b, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 45 * time.Second, MaxHeaderBytes: 8192}
	fmt.Println("owner-bound history reader ready")
	if err = server.Serve(listener); err != nil && err != http.ErrServerClosed {
		log.Fatal("history server stopped")
	}
}
