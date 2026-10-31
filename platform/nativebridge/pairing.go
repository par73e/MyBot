package main

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/BurntSushi/toml"
	"github.com/joho/godotenv"
	"reasonix/internal/config"
)

type instancePairing struct {
	Account string `json:"account"`
	User    string `json:"user"`
	Token   string `json:"token"`
	Base    string `json:"base"`
}

type instanceAccount struct {
	Token   string `json:"token"`
	BaseURL string `json:"base_url"`
	UserID  string `json:"user_id"`
	SavedAt string `json:"saved_at"`
}

// instanceOwnership is exposed only over the authenticated local socket. It
// returns the account identifier, never the credential or WeChat user ID.
func (b *bridge) instanceOwnership() (map[string]any, error) {
	if !isolatedRuntime {
		return nil, errors.New("instance ownership unavailable")
	}
	cfg, err := readConfig()
	if err != nil {
		return nil, err
	}
	id := cfg.Bot.Weixin.AccountID
	users := cfg.Bot.Allowlist.WeixinUsers
	if !cfg.Bot.Weixin.Enabled || len(users) != 1 || !validPairText(id, 240) || !filepath.IsLocal(id) || filepath.Base(id) != id || strings.ContainsAny(id, `/\`) {
		return nil, errors.New("instance ownership needs review")
	}
	dir := filepath.Join(home, "weixin", "accounts")
	for _, parent := range []string{filepath.Join(home, "weixin"), dir} {
		info, statErr := os.Lstat(parent)
		if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("instance credential unavailable")
		}
	}
	path := filepath.Join(dir, id+".json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("instance credential unavailable")
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) > 16384 {
		return nil, errors.New("instance credential unavailable")
	}
	var saved instanceAccount
	if json.Unmarshal(raw, &saved) != nil || saved.Token == "" || saved.UserID != users[0] {
		return nil, errors.New("instance ownership needs review")
	}
	environment, envErr := godotenv.Read(filepath.Join(home, ".env"))
	if envErr != nil && !os.IsNotExist(envErr) {
		return nil, errors.New("instance credential needs review")
	}
	for _, value := range []string{os.Getenv(cfg.Bot.Weixin.TokenEnv), environment[cfg.Bot.Weixin.TokenEnv]} {
		if value != "" && subtle.ConstantTimeCompare([]byte(value), []byte(saved.Token)) != 1 {
			return nil, errors.New("instance credential override needs review")
		}
	}
	return map[string]any{"account": id, "credential_configured": true}, nil
}

func validPairText(value string, max int) bool {
	if value == "" || len(value) > max {
		return false
	}
	for _, ch := range value {
		if unicode.IsControl(ch) {
			return false
		}
	}
	return true
}

func (b *bridge) pairInstance(input instancePairing) (map[string]bool, error) {
	if !isolatedRuntime {
		return nil, errors.New("pairing unavailable")
	}
	if !validPairText(input.Account, 240) || !filepath.IsLocal(input.Account) || filepath.Base(input.Account) != input.Account || strings.ContainsAny(input.Account, `/\`) || !validPairText(input.User, 512) || !validPairText(input.Token, 8192) {
		return nil, errors.New("invalid pairing")
	}
	u, err := url.Parse(input.Base)
	if err != nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") || !(u.Hostname() == "ilinkai.weixin.qq.com" || strings.HasSuffix(u.Hostname(), ".weixin.qq.com")) || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid pairing endpoint")
	}
	b.profileMu.Lock()
	defer b.profileMu.Unlock()
	raw, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		return nil, err
	}
	cfg := map[string]any{}
	if _, err = toml.Decode(string(raw), &cfg); err != nil {
		return nil, err
	}
	bot, ok := cfg["bot"].(map[string]any)
	if !ok {
		return nil, errors.New("bot config missing")
	}
	allow, ok := bot["allowlist"].(map[string]any)
	if !ok {
		return nil, errors.New("allowlist missing")
	}
	if users, exists := allow["weixin_users"]; exists {
		switch value := users.(type) {
		case []any:
			if len(value) > 1 || (len(value) == 1 && value[0] != input.User) {
				return nil, errors.New("existing binding requires reconciliation")
			}
		case []string:
			if len(value) > 1 || (len(value) == 1 && value[0] != input.User) {
				return nil, errors.New("existing binding requires reconciliation")
			}
		default:
			return nil, errors.New("invalid existing binding")
		}
	}
	weixin, ok := bot["weixin"].(map[string]any)
	if !ok {
		weixin = map[string]any{}
		bot["weixin"] = weixin
	}
	if current, ok := weixin["account_id"].(string); ok && current != "" && current != input.Account {
		return nil, errors.New("existing account requires reconciliation")
	}
	path := filepath.Join(home, "weixin", "accounts", input.Account+".json")
	if existing, err := os.ReadFile(path); err == nil {
		var account instanceAccount
		if json.Unmarshal(existing, &account) != nil || account.UserID != input.User || account.BaseURL != input.Base || subtle.ConstantTimeCompare([]byte(account.Token), []byte(input.Token)) != 1 {
			return nil, errors.New("existing credential requires reconciliation")
		}
	} else if os.IsNotExist(err) {
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		data, err := json.Marshal(instanceAccount{Token: input.Token, BaseURL: input.Base, UserID: input.User, SavedAt: time.Now().UTC().Format(time.RFC3339)})
		if err != nil {
			return nil, err
		}
		if err = atomicWrite(path, data); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	allow["enabled"], allow["allow_all"], allow["weixin_users"] = true, false, []string{input.User}
	allow["weixin_admins"], allow["weixin_approvers"] = []string{input.User}, []string{input.User}
	weixin["enabled"], weixin["account_id"], weixin["api_base"] = true, input.Account, input.Base
	bot["enabled"] = true
	var out bytes.Buffer
	if err = toml.NewEncoder(&out).Encode(cfg); err != nil {
		return nil, err
	}
	var check config.Config
	if _, err = toml.Decode(out.String(), &check); err != nil {
		return nil, err
	}
	if err = atomicWrite(filepath.Join(home, "config.toml"), out.Bytes()); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}
