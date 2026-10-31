// Private control facade over the owner's existing Friday. It neither copies
// credentials nor starts a second bot, poller, or Heartbeat scheduler.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/joho/godotenv"
	"reasonix/internal/assistant"
	"reasonix/internal/config"
	"reasonix/internal/heartbeat"
	"reasonix/internal/session"
	"reasonix/internal/tool"
)

const alias = "friday_full"

type bridge struct {
	token        string
	chatMu       sync.Mutex
	profileMu    sync.Mutex
	lifecycleMu  sync.Mutex
	cancelMu     sync.Mutex
	activeCancel context.CancelFunc
	paused       bool
}
type call struct {
	Tool string          `json:"tool"`
	Args json.RawMessage `json:"args"`
}

func hash(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func readConfig() (*config.Config, error) {
	c, e := config.LoadUserConfigReadOnly()
	if e != nil {
		return nil, e
	}
	if len(c.Bot.Allowlist.WeixinUsers) > 1 || (!isolatedRuntime && len(c.Bot.Allowlist.WeixinUsers) != 1) {
		return nil, errors.New("expected the original single owner")
	}
	return c, nil
}
func registry() ([]tool.Tool, error) {
	c, e := readConfig()
	if e != nil {
		return nil, e
	}
	options := assistant.Options{Workspace: workspace, Model: c.DefaultModel}
	if len(c.Bot.Allowlist.WeixinUsers) == 1 {
		options.Target = &heartbeat.Target{ConnectionID: "weixin", Domain: "weixin", ChatID: c.Bot.Allowlist.WeixinUsers[0], ChatType: "dm"}
	}
	tools := assistant.Tools(home, options)
	tools = append(tools,
		assistant.HeartbeatTool{Service: assistant.HeartbeatService{Home: home}},
		assistant.MailTool{Service: assistant.NewMailService(home)},
		assistant.WeatherTool{Service: assistant.NewWeatherService(home)},
		assistant.CourseTool{Service: assistant.CourseService{Home: home}},
		assistant.NewsTool{Service: assistant.HotNewsService{Home: home}},
		assistant.MorningTool{Service: assistant.MorningService{Home: home}},
		assistant.EveningTool{Service: assistant.EveningService{Home: home}},
		assistant.SubscriptionTool{Service: assistant.Subscriptions{Repository: assistant.NewRepository(home)}},
		assistant.GitHubTool{Service: assistant.GitHubService{Home: home}},
		assistant.AlertTool{Service: assistant.AlertService{Home: home}},
		assistant.ProposalTool{Service: assistant.Proposals{Home: home, Options: options}},
	)
	if isolatedRuntime && options.Target != nil {
		dir := config.ProjectSessionDir(workspace)
		if dir == "" {
			return nil, errors.New("instance session directory unavailable")
		}
		tools = append(tools, assistant.HistoryTool{Repository: assistant.NewRepository(home), Owner: assistant.ConversationOwner(options.Target.ConnectionID, options.Target.ChatID, workspace), Root: session.RootForLegacyDir(dir), LegacySessionID: assistant.LegacyConversationID(options.Target.ConnectionID, options.Target.ChatID)})
	}
	return tools, nil
}
func execute(ctx context.Context, in call) (any, error) {
	tools, e := registry()
	if e != nil {
		return nil, e
	}
	for _, t := range tools {
		if t.Name() == in.Tool {
			// An explicit authenticated settings form is equivalent to the native
			// `assistant memory` CLI. Model-driven writes still use the native Agent's
			// foreground authorization; this path never accepts model-generated calls.
			if in.Tool == "assistant_memory" {
				var r assistant.MemoryRequest
				d := json.NewDecoder(bytes.NewReader(in.Args))
				d.DisallowUnknownFields()
				if e = d.Decode(&r); e != nil {
					return nil, e
				}
				return (assistant.Memories{Home: home}).Apply(ctx, r)
			}
			raw, e := t.Execute(ctx, in.Args)
			if e != nil {
				return nil, e
			}
			var result any
			e = json.Unmarshal([]byte(raw), &result)
			return result, e
		}
	}
	return nil, errors.New("unknown module")
}
func sensitive(k string) bool {
	k = strings.ToLower(strings.ReplaceAll(k, "-", "_"))
	return k == "token" || k == "password" || k == "authorization" || k == "proxy_authorization" || k == "cookie" || k == "set_cookie" || k == "bearer" || k == "credentials" || k == "authorization_code" || k == "credential_file" || k == "client_secret" || k == "api_key" || k == "apikey" || k == "secrets" || k == "access_token" || k == "refresh_token" || k == "secret" || k == "private_key" || strings.HasSuffix(k, "_key") || strings.HasSuffix(k, "_token") || strings.HasSuffix(k, "secret") || strings.HasSuffix(k, "password")
}
func public(value any) any {
	raw, e := json.Marshal(value)
	if e != nil {
		return nil
	}
	var normalized any
	if json.Unmarshal(raw, &normalized) != nil {
		return nil
	}
	return clean(normalized)
}
func configured(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case string:
		return v != ""
	case map[string]any:
		return len(v) > 0
	case []any:
		return len(v) > 0
	case bool:
		return v
	default:
		return true
	}
}
func clean(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, item := range v {
			if sensitive(k) {
				out[k+"_configured"] = configured(item)
			} else {
				out[k] = clean(item)
			}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = clean(item)
		}
		return out
	case string:
		if !strings.ContainsAny(v, " \r\n\t") {
			if u, e := url.Parse(v); e == nil && u.Host != "" && u.Scheme != "" {
				u.User = nil
				q := u.Query()
				changed := false
				for k := range q {
					if sensitive(k) || strings.EqualFold(k, "key") {
						q.Set(k, "[已隐藏]")
						changed = true
					}
				}
				if changed {
					u.RawQuery = q.Encode()
				}
				return u.String()
			}
		}
		return v
	default:
		return v
	}
}
func (b *bridge) snapshot(ctx context.Context) (any, error) {
	c, e := readConfig()
	if e != nil {
		return nil, e
	}
	result := map[string]any{"model_ref": c.DefaultModel, "mode": "original_friday", "scheduler": "existing_only", "data_moved": false}
	if isolatedRuntime {
		result["mode"] = "user_instance"
		result["scheduler"] = "instance"
		delete(result, "data_moved")
	}
	original := map[string]any{}
	if _, e = toml.DecodeFile(home+"/config.toml", &original); e != nil {
		return nil, e
	}
	result["configuration"] = public(original)
	if isolatedRuntime {
		if _, err := os.Stat(filepath.Join(home, "assistant", "state.sqlite3")); os.IsNotExist(err) {
			// A new assistant must be configurable before its first business
			// write creates the database or before WeChat has been paired.
			result["initialized"] = false
			running, controlErr := b.instanceControl(ctx, "status")
			if controlErr != nil {
				return nil, controlErr
			}
			result["running"] = running.Running
			result["desired_running"] = running.DesiredRunning
			result["wechat_configured"] = c.Bot.Weixin.Enabled && len(c.Bot.Allowlist.WeixinUsers) == 1
			result["counts"] = map[string]int{}
			result["files"] = []map[string]any{}
			result["stored_configs"] = []map[string]any{}
			return result, nil
		} else if err != nil {
			return nil, err
		}
	}
	files := []map[string]any{}
	// Config files are selected on the host. No frontend path is ever opened.
	entries, e := os.ReadDir(home + "/assistant")
	if e != nil {
		return nil, e
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, e := os.ReadFile(filepath.Join(home, "assistant", entry.Name()))
		if e != nil {
			return nil, e
		}
		var v any
		if json.Unmarshal(raw, &v) == nil {
			files = append(files, map[string]any{"name": entry.Name(), "sha256": hash(raw), "config": public(v)})
		}
	}
	result["files"] = files
	db, e := sql.Open("sqlite", "file:"+home+"/assistant/state.sqlite3?mode=ro")
	if e != nil {
		return nil, e
	}
	defer db.Close()
	rows, e := db.QueryContext(ctx, "SELECT kind,count(*) FROM assistant_entities GROUP BY kind")
	if e != nil {
		return nil, e
	}
	counts := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if e = rows.Scan(&k, &n); e != nil {
			rows.Close()
			return nil, e
		}
		counts[k] = n
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	result["counts"] = counts
	configRows, e := db.QueryContext(ctx, "SELECT kind,id,payload FROM assistant_entities WHERE kind LIKE '%_config' OR kind IN ('assistant_policy','expiry_settings')")
	if e != nil {
		return nil, e
	}
	stored := []map[string]any{}
	for configRows.Next() {
		var kind, id, raw string
		if e = configRows.Scan(&kind, &id, &raw); e != nil {
			configRows.Close()
			return nil, e
		}
		var v any
		if json.Unmarshal([]byte(raw), &v) == nil {
			stored = append(stored, map[string]any{"kind": kind, "id": id, "config": public(v)})
		}
	}
	e = configRows.Err()
	configRows.Close()
	if e != nil {
		return nil, e
	}
	result["stored_configs"] = stored
	var mailConfig map[string]any
	raw, e := os.ReadFile(home + "/assistant/mail.json")
	if e == nil && json.Unmarshal(raw, &mailConfig) == nil {
		accounts := []map[string]any{}
		if list, ok := mailConfig["accounts"].([]any); ok {
			for _, item := range list {
				a, ok := item.(map[string]any)
				if !ok {
					continue
				}
				path, _ := a["credential_file"].(string)
				info, err := os.Stat(path)
				accounts = append(accounts, map[string]any{"id": a["id"], "address": a["address"], "provider": a["provider"], "forwarded_accounts": a["forwarded_accounts"], "credential_configured": err == nil && info.Mode().IsRegular()})
			}
		}
		result["mail_accounts"] = accounts
	}
	for key, name := range map[string]string{"mail": "assistant_mail", "heartbeat": "assistant_heartbeat"} {
		action := "status"
		if key == "heartbeat" {
			action = "get"
		}
		v, e := execute(ctx, call{Tool: name, Args: json.RawMessage(`{"action":"` + action + `"}`)})
		if e != nil {
			result[key] = map[string]string{"error": "状态读取失败"}
		} else {
			result[key] = public(v)
		}
	}
	memories, e := (assistant.Memories{Home: home}).Apply(ctx, assistant.MemoryRequest{Action: "list", IncludeInactive: true})
	if e == nil {
		result["memory_count"] = len(memories)
	}
	if isolatedRuntime {
		running, err := b.instanceControl(ctx, "status")
		if err != nil {
			return nil, err
		}
		result["running"] = running.Running
		result["desired_running"] = running.DesiredRunning
	} else {
		active, _ := exec.CommandContext(ctx, "systemctl", "is-active", serviceUnit).Output()
		result["running"] = strings.TrimSpace(string(active)) == "active"
	}
	result["wechat_configured"] = c.Bot.Weixin.Enabled && len(c.Bot.Allowlist.WeixinUsers) == 1
	result["source_config_hash"] = hash(mustRead(home + "/config.toml"))
	var verification any
	if json.Unmarshal(mustRead(state+"/mail-verification.json"), &verification) == nil {
		result["mail_verification"] = verification
	}
	return result, nil
}
func mustRead(path string) []byte { raw, _ := os.ReadFile(path); return raw }
func resolvePromptPath(path, root, userHome string) (string, error) {
	if path == "" {
		return "", errors.New("original prompt file is missing")
	}
	if filepath.IsAbs(path) {
		return path, nil
	}
	for _, base := range []string{root, userHome} {
		candidate := filepath.Join(base, path)
		if info, e := os.Stat(candidate); e == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}
	return "", errors.New("original prompt file unavailable")
}
func (b *bridge) profile() (any, error) {
	c, e := readConfig()
	if e != nil {
		return nil, e
	}
	path, e := resolvePromptPath(c.Agent.SystemPromptFile, workspace, home)
	if e != nil {
		return nil, e
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	providers := []map[string]any{}
	keyConfigured := false
	environment, _ := godotenv.Read(home + "/.env")
	cfg := map[string]any{}
	if _, e = toml.DecodeFile(home+"/config.toml", &cfg); e != nil {
		return nil, e
	}
	if list, ok := cfg["providers"].([]map[string]any); ok {
		for _, p := range list {
			providers = append(providers, map[string]any{"name": p["name"], "model": p["model"], "default_effort": p["default_effort"], "supported_efforts": p["supported_efforts"]})
			if name, ok := p["name"].(string); ok && strings.HasPrefix(c.DefaultModel, name+"/") {
				variable, _ := p["api_key_env"].(string)
				keyConfigured = environment[variable] != "" || os.Getenv(variable) != ""
			}
		}
	}
	// BurntSushi represents TOML arrays of tables as []map[string]interface{}.
	revisionData := append(append(mustRead(home+"/config.toml"), raw...), mustRead(home+"/.env")...)
	return map[string]any{"prompt": string(raw), "revision": hash(revisionData), "model_ref": c.DefaultModel, "providers": providers, "prompt_bytes": len(raw), "key_configured": keyConfigured}, nil
}
func atomicWrite(path string, raw []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".asrobot-write-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(raw)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, path)
}
func (b *bridge) updateProfile(in map[string]json.RawMessage) (any, error) {
	b.profileMu.Lock()
	defer b.profileMu.Unlock()
	current, e := b.profile()
	if e != nil {
		return nil, e
	}
	p := current.(map[string]any)
	var rev string
	if json.Unmarshal(in["revision"], &rev) != nil || rev != p["revision"] {
		return nil, errors.New("configuration revision changed; refresh first")
	}
	for k := range in {
		if k != "revision" && k != "prompt" && k != "model_ref" && k != "reasoning_effort" && k != "api_key" {
			return nil, errors.New("unsupported profile field")
		}
	}
	if _, ok := in["model_ref"]; !ok {
		if _, ok := in["api_key"]; ok {
			return nil, errors.New("model_ref required")
		}
		if _, ok := in["reasoning_effort"]; ok {
			return nil, errors.New("model_ref required")
		}
	}
	c, e := readConfig()
	if e != nil {
		return nil, e
	}
	path, e := resolvePromptPath(c.Agent.SystemPromptFile, workspace, home)
	if e != nil {
		return nil, e
	}
	oldPrompt := mustRead(path)
	oldConfig := mustRead(home + "/config.toml")
	oldEnv := mustRead(home + "/.env")
	newPrompt, newConfig, newEnv := oldPrompt, oldConfig, oldEnv
	if v, ok := in["prompt"]; ok {
		var prompt string
		if json.Unmarshal(v, &prompt) != nil || len(prompt) == 0 || len(prompt) > 48000 {
			return nil, errors.New("invalid prompt")
		}
		newPrompt = []byte(prompt)
	}
	if _, ok := in["model_ref"]; ok {
		cfg := map[string]any{}
		if _, e = toml.Decode(string(oldConfig), &cfg); e != nil {
			return nil, e
		}
		env, e := godotenv.Unmarshal(string(oldEnv))
		if e != nil {
			return nil, e
		}
		if e = changeModel(cfg, env, in); e != nil {
			return nil, e
		}
		var out bytes.Buffer
		if e = toml.NewEncoder(&out).Encode(cfg); e != nil {
			return nil, e
		}
		newConfig = out.Bytes()
		// Validate serialization against the actual native config type before write.
		var check config.Config
		if _, e = toml.Decode(string(newConfig), &check); e != nil {
			return nil, e
		}
		if _, ok := in["api_key"]; ok {
			value, e := godotenv.Marshal(env)
			if e != nil {
				return nil, e
			}
			newEnv = []byte(value + "\n")
		}
	}
	files := []struct {
		path     string
		old, new []byte
	}{{path, oldPrompt, newPrompt}, {home + "/config.toml", oldConfig, newConfig}, {home + "/.env", oldEnv, newEnv}}
	for i, f := range files {
		if bytes.Equal(f.old, f.new) {
			continue
		}
		if e = atomicWrite(f.path, f.new); e != nil {
			for _, previous := range files[:i] {
				_ = atomicWrite(previous.path, previous.old)
			}
			return nil, e
		}
	}
	return map[string]any{"ok": true, "requires_apply": true}, nil
}
func changeModel(cfg map[string]any, env map[string]string, in map[string]json.RawMessage) error {
	var ref string
	if json.Unmarshal(in["model_ref"], &ref) != nil || len(ref) > 240 {
		return errors.New("invalid model")
	}
	name, model, ok := strings.Cut(ref, "/")
	if !ok || model == "" || strings.ContainsAny(model, "\r\n") {
		return errors.New("invalid model")
	}
	providers, ok := cfg["providers"].([]map[string]any)
	if !ok {
		return errors.New("original providers unavailable")
	}
	var selected map[string]any
	for _, p := range providers {
		if p["name"] == name {
			selected = p
			break
		}
	}
	if selected == nil {
		return errors.New("unknown existing provider")
	}
	if v, ok := in["reasoning_effort"]; ok {
		var effort string
		if json.Unmarshal(v, &effort) != nil {
			return errors.New("invalid effort")
		}
		if effort != "" && effort != "low" && effort != "medium" && effort != "high" && effort != "xhigh" {
			return errors.New("invalid effort")
		}
		selected["default_effort"] = effort
	}
	if v, ok := in["api_key"]; ok {
		var key string
		if json.Unmarshal(v, &key) != nil || len(key) == 0 || len(key) > 1024 || strings.ContainsAny(key, "\r\n") {
			return errors.New("invalid key")
		}
		variable, _ := selected["api_key_env"].(string)
		if !regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`).MatchString(variable) {
			return errors.New("provider does not use a writable environment credential")
		}
		env[variable] = key
	}
	cfg["default_model"] = ref
	selected["model"] = model
	bot, ok := cfg["bot"].(map[string]any)
	if !ok {
		return errors.New("original bot configuration unavailable")
	}
	bot["model"] = ref
	return nil
}

type cappedBuffer struct {
	bytes.Buffer
	limit int
}

func (w *cappedBuffer) Write(p []byte) (int, error) {
	if w.Len()+len(p) > w.limit {
		return 0, errors.New("output limit")
	}
	return w.Buffer.Write(p)
}
func (w *cappedBuffer) ReadFrom(r io.Reader) (int64, error) {
	return io.Copy(struct{ io.Writer }{w}, r)
}

var validSession = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,200}$`)

func (b *bridge) chat(ctx context.Context, text string) (any, error) {
	b.chatMu.Lock()
	defer b.chatMu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	b.cancelMu.Lock()
	if b.paused {
		b.cancelMu.Unlock()
		return nil, errors.New("original runtime paused")
	}
	b.activeCancel = cancel
	b.cancelMu.Unlock()
	defer func() { b.cancelMu.Lock(); b.activeCancel = nil; b.cancelMu.Unlock() }()
	c, e := readConfig()
	if e != nil {
		return nil, e
	}
	mode := c.Bot.ToolApprovalMode
	if mode == "" {
		mode = "workspace-write"
	}
	if mode != "workspace-write" && mode != "read-only" && mode != "danger-full-access" {
		return nil, errors.New("original permission mode requires a supported headless policy")
	}
	args := []string{"run", "--dir", workspace, "--output-format", "json", "--permission-mode", mode}
	if c.Bot.MaxSteps > 0 {
		args = append(args, "--max-steps", strconv.Itoa(c.Bot.MaxSteps))
	}
	var saved struct {
		ID string `json:"session_id"`
	}
	path := state + "/web-session.json"
	raw, _ := os.ReadFile(path)
	_ = json.Unmarshal(raw, &saved)
	if saved.ID != "" {
		if !validSession.MatchString(saved.ID) {
			return nil, errors.New("invalid saved session")
		}
		args = append(args, "--resume", saved.ID)
	}
	cmd := exec.CommandContext(ctx, executablePath, args...)
	cmd.Dir = workspace
	cmd.Stdin = strings.NewReader(text)
	env := map[string]string{}
	for _, v := range os.Environ() {
		k, value, ok := strings.Cut(v, "=")
		if ok {
			env[k] = value
		}
	}
	if current, e := godotenv.Read(home + "/.env"); e == nil {
		for k, v := range current {
			env[k] = v
		}
	}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
		return nil
	}
	cmd.WaitDelay = 5 * time.Second
	stdout := &cappedBuffer{limit: 8 << 20}
	stderr := &cappedBuffer{limit: 128 << 10}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	runErr := cmd.Run()
	var answer struct {
		Result    string `json:"result"`
		IsError   bool   `json:"is_error"`
		Reasoning bool   `json:"result_from_reasoning"`
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(stdout.Bytes(), &answer) != nil {
		return nil, errors.New("助手本轮没有返回可见答复，请稍后重试")
	}
	if answer.SessionID != "" && validSession.MatchString(answer.SessionID) {
		v, _ := json.Marshal(map[string]string{"session_id": answer.SessionID})
		if e = atomicWrite(path, v); e != nil {
			return nil, e
		}
	}
	if runErr != nil || answer.IsError || answer.Reasoning || strings.TrimSpace(answer.Result) == "" {
		return nil, errors.New("助手本轮未完成，请查看运行状态后重试")
	}
	engine := "original_friday"
	if isolatedRuntime {
		engine = "user_instance"
	}
	return map[string]any{"text": answer.Result, "engine": engine}, nil
}
func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (b *bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+b.token)) != 1 {
		respond(w, 403, map[string]string{"error": "denied"})
		return
	}
	if r.URL.Path == "/health" && r.Method == "GET" {
		respond(w, 200, map[string]string{"status": "ok"})
		return
	}
	if r.URL.Query().Get("source") != alias {
		respond(w, 404, map[string]string{"error": "unknown source"})
		return
	}
	duration := 50 * time.Second
	if r.URL.Path == "/v1/chat" {
		duration = 180 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), duration)
	defer cancel()
	var result any
	var err error
	switch {
	case r.Method == "GET" && r.URL.Path == "/v1/status":
		result, err = b.snapshot(ctx)
	case r.Method == "GET" && r.URL.Path == "/v1/profile":
		result, err = b.profile()
	case r.Method == "GET" && r.URL.Path == "/v1/identity":
		result, err = b.identity()
	case r.Method == "GET" && r.URL.Path == "/v1/ownership":
		result, err = b.instanceOwnership()
	case r.Method == "GET" && r.URL.Path == "/v1/mail-accounts":
		result, err = b.mailAccounts()
	case r.Method == "GET" && r.URL.Path == "/v1/weather-setup":
		result, err = b.weatherSetupStatus()
	case r.Method == "GET" && r.URL.Path == "/v1/github-credential":
		result, err = b.githubCredentialStatus()
	case r.Method == "GET" && r.URL.Path == "/v1/tools":
		var tools []tool.Tool
		tools, err = registry()
		out := []map[string]any{}
		for _, t := range tools {
			out = append(out, map[string]any{"name": t.Name(), "schema": t.Schema(), "description": t.Description()})
		}
		sort.Slice(out, func(i, j int) bool { return out[i]["name"].(string) < out[j]["name"].(string) })
		result = out
	case r.Method == "POST":
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
		decoder.DisallowUnknownFields()
		switch r.URL.Path {
		case "/v1/tool":
			var in call
			if err = decoder.Decode(&in); err == nil {
				result, err = execute(ctx, in)
			}
		case "/v1/chat":
			var in struct {
				Text string `json:"text"`
			}
			if err = decoder.Decode(&in); err == nil {
				if len(in.Text) == 0 || len(in.Text) > 32000 {
					err = errors.New("invalid text")
				} else {
					result, err = b.chat(ctx, in.Text)
				}
			}
		case "/v1/profile":
			var in map[string]json.RawMessage
			if err = decoder.Decode(&in); err == nil {
				result, err = b.updateProfile(in)
			}
		case "/v1/identity":
			var in identityUpdate
			if err = decoder.Decode(&in); err == nil {
				result, err = b.updateIdentity(in)
			}
		case "/v1/mail-accounts":
			var in mailAccountInput
			if err = decoder.Decode(&in); err == nil {
				result, err = b.updateMailAccount(in)
			}
		case "/v1/weather-setup":
			var in weatherSetupInput
			if err = decoder.Decode(&in); err == nil {
				result, err = b.configureWeather(in)
			}
		case "/v1/github-credential":
			var in githubCredentialInput
			if err = decoder.Decode(&in); err == nil {
				result, err = b.updateGitHubCredential(in)
			}
		case "/v1/pair-wechat":
			var in instancePairing
			if err = decoder.Decode(&in); err == nil {
				b.lifecycleMu.Lock()
				if isolatedRuntime {
					var running instanceControlResponse
					running, err = b.instanceControl(ctx, "status")
					if err == nil && (running.Running || running.DesiredRunning) {
						err = errors.New("assistant must be stopped before pairing")
					}
				}
				if err == nil {
					result, err = b.pairInstance(in)
				}
				b.lifecycleMu.Unlock()
			}
		case "/v1/mail-test":
			var raw []byte
			raw, err = exec.CommandContext(ctx, "python3", mailcheckPath).Output()
			if err == nil {
				err = json.Unmarshal(raw, &result)
				if err == nil {
					err = atomicWrite(state+"/mail-verification.json", raw)
				}
			}
		case "/v1/apply", "/v1/pause", "/v1/resume":
			b.lifecycleMu.Lock()
			// Polkit permits lifecycle operations on this one owned service only.
			// No client-selected service name or command is ever accepted.
			action := "restart"
			if r.URL.Path == "/v1/pause" {
				action = "stop"
				b.cancelMu.Lock()
				b.paused = true
				if b.activeCancel != nil {
					b.activeCancel()
				}
				b.cancelMu.Unlock()
			}
			if r.URL.Path == "/v1/resume" {
				action = "start"
			}
			if isolatedRuntime {
				_, err = b.instanceControl(ctx, action)
			} else {
				err = exec.CommandContext(ctx, "/usr/bin/systemctl", "--no-ask-password", action, serviceUnit).Run()
			}
			if err == nil && action != "stop" {
				b.cancelMu.Lock()
				b.paused = false
				b.cancelMu.Unlock()
			}
			result = map[string]bool{"ok": err == nil}
			b.lifecycleMu.Unlock()
		default:
			respond(w, 404, map[string]string{"error": "not found"})
			return
		}
	default:
		respond(w, 404, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		// Never forward subprocess stderr, paths, credentials, or hidden reasoning.
		message := "助手操作未完成，请检查输入或稍后重试"
		status := 400
		if strings.Contains(err.Error(), "revision") {
			message = "设置已变化，请刷新后重试"
			status = 409
		}
		if r.URL.Path == "/v1/chat" {
			message = "助手本轮未完成，请先查看记录和模型状态，再决定是否重试"
			status = 502
		}
		if r.Method == "GET" {
			message = "助手状态暂时无法读取"
			status = 503
		}
		respond(w, status, map[string]string{"error": message})
		return
	}
	respond(w, 200, public(result))
}
func main() {
	if e := configureRuntime(os.Getenv); e != nil {
		log.Fatal(e)
	}
	if os.Getenv("REASONIX_HOME") != home || !assistant.Enabled() {
		log.Fatal("original runtime environment required")
	}
	raw, e := os.ReadFile(tokenPath)
	if e != nil {
		log.Fatal("private token unavailable")
	}
	b := &bridge{token: strings.TrimSpace(string(raw))}
	if len(b.token) < 32 {
		log.Fatal("invalid private token")
	}
	if _, e = readConfig(); e != nil {
		log.Fatal("original owner configuration unavailable")
	}
	path := socketPath
	if e = prepareSocket(path); e != nil {
		log.Fatal("socket path unavailable")
	}
	ln, e := net.Listen("unix", path)
	if e != nil {
		log.Fatal("socket unavailable")
	}
	if e = os.Chmod(path, 0660); e != nil {
		log.Fatal("socket mode failed")
	}
	if isolatedRuntime {
		if uid := os.Getenv("ASROBOT_PLATFORM_UID"); uid != "" {
			if !regexp.MustCompile(`^[1-9][0-9]{0,9}$`).MatchString(uid) {
				log.Fatal("invalid platform UID")
			}
			if e = exec.Command("/usr/bin/setfacl", "-m", "u:"+uid+":rw-", path).Run(); e != nil {
				log.Fatal("platform socket ACL failed")
			}
		}
	}
	server := &http.Server{Handler: b, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 190 * time.Second, MaxHeaderBytes: 8192}
	fmt.Println("owner-bound original Friday facade ready")
	if e = server.Serve(ln); e != nil && e != http.ErrServerClosed {
		log.Fatal("native facade stopped")
	}
}

var _ io.Writer = (*cappedBuffer)(nil)
