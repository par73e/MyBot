package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

type gmailCredentialInput struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RefreshToken string `json:"refresh_token"`
	AccessToken  string `json:"access_token"`
	ExpiresAt    int64  `json:"expires_at"`
}
type mailAccountInput struct {
	Revision          string               `json:"revision"`
	ID                string               `json:"id"`
	Provider          string               `json:"provider"`
	Address           string               `json:"address"`
	AuthorizationCode string               `json:"authorization_code"`
	Gmail             gmailCredentialInput `json:"gmail"`
	UseProxy          bool                 `json:"use_proxy"`
}

func privateMailDir() (string, error) {
	parent := filepath.Join(home, "assistant")
	if err := os.MkdirAll(parent, 0700); err != nil {
		return "", err
	}
	for _, path := range []string{parent, filepath.Join(parent, "mail-credentials")} {
		if err := os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
			return "", err
		}
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
			return "", errors.New("mail credential directory is not private")
		}
	}
	return filepath.Join(parent, "mail-credentials"), nil
}

func readMailConfiguration() (map[string]any, []byte, error) {
	if !isolatedRuntime {
		return nil, nil, errors.New("mail setup unavailable")
	}
	parent := filepath.Join(home, "assistant")
	if info, err := os.Lstat(parent); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, nil, errors.New("mail configuration unavailable")
		}
	} else if !os.IsNotExist(err) {
		return nil, nil, err
	}
	path := filepath.Join(home, "assistant", "mail.json")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return map[string]any{"enabled": true, "poll_seconds": 60, "accounts": []any{}}, nil, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, nil, errors.New("mail configuration unavailable")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil || data == nil {
		return nil, nil, errors.New("invalid mail configuration")
	}
	if _, ok := data["accounts"].([]any); !ok {
		return nil, nil, errors.New("invalid mail configuration")
	}
	return data, raw, nil
}

func (b *bridge) mailAccounts() (map[string]any, error) {
	data, raw, err := readMailConfiguration()
	if err != nil {
		return nil, err
	}
	accounts := []map[string]any{}
	for _, item := range data["accounts"].([]any) {
		a, ok := item.(map[string]any)
		if !ok {
			return nil, errors.New("invalid mail configuration")
		}
		id, okID := a["id"].(string)
		address, okAddress := a["address"].(string)
		provider, okProvider := a["provider"].(string)
		if !okID || !okAddress || !okProvider {
			return nil, errors.New("invalid mail configuration")
		}
		proxy, _ := a["proxy"].(string)
		accounts = append(accounts, map[string]any{"id": id, "address": address, "provider": provider, "uses_proxy": proxy != ""})
	}
	return map[string]any{"revision": hash(raw), "accounts": accounts, "proxy_available": privateMailRelayPort() != 0}, nil
}

func privateMailRelayPort() int {
	port, err := strconv.Atoi(os.Getenv("ASROBOT_MAIL_PROXY_PORT"))
	root := os.Getenv("ASROBOT_RUNTIME_ROOT")
	state := os.Getenv("ASROBOT_RUNTIME_STATE")
	socketPath := os.Getenv("ASROBOT_MAIL_PROXY_SOCKET")
	if err != nil || port < 1 || port > 65535 || !filepath.IsAbs(root) || filepath.Clean(root) != root ||
		state != filepath.Join(root, "state") || socketPath != filepath.Join(state, "mail-proxy.sock") {
		return 0
	}
	info, err := os.Lstat(socketPath)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return 0
	}
	return port
}

func validMailSecret(value string, required bool) bool {
	if required && strings.TrimSpace(value) == "" {
		return false
	}
	if len(value) > 8192 {
		return false
	}
	for _, ch := range value {
		if unicode.IsControl(ch) {
			return false
		}
	}
	return true
}

func (b *bridge) updateMailAccount(in mailAccountInput) (map[string]any, error) {
	if !isolatedRuntime {
		return nil, errors.New("mail setup unavailable")
	}
	b.profileMu.Lock()
	defer b.profileMu.Unlock()
	data, raw, err := readMailConfiguration()
	if err != nil {
		return nil, err
	}
	if in.Revision != hash(raw) {
		return nil, errors.New("mail configuration revision changed; refresh first")
	}
	in.Address = strings.TrimSpace(in.Address)
	parsed, err := mail.ParseAddress(in.Address)
	if err != nil || parsed.Address != in.Address || parsed.Name != "" || len(in.Address) > 320 || (in.Provider != "qq" && in.Provider != "gmail") {
		return nil, errors.New("invalid mail account")
	}
	var credential map[string]any
	if in.Provider == "qq" {
		if in.UseProxy || !validMailSecret(in.AuthorizationCode, true) {
			return nil, errors.New("invalid QQ mail credential")
		}
		credential = map[string]any{"account": in.Address, "authorization_code": in.AuthorizationCode}
	} else {
		g := in.Gmail
		if !validMailSecret(g.ClientID, true) || !validMailSecret(g.ClientSecret, true) || !validMailSecret(g.RefreshToken, true) || !validMailSecret(g.AccessToken, false) || g.ExpiresAt < 0 {
			return nil, errors.New("invalid Gmail credential")
		}
		credential = map[string]any{"account": in.Address, "client_id": g.ClientID, "client_secret": g.ClientSecret, "token": map[string]any{"refresh_token": g.RefreshToken, "access_token": g.AccessToken, "expires_at": g.ExpiresAt}}
	}
	proxy := ""
	if in.UseProxy {
		port := privateMailRelayPort()
		if port == 0 {
			return nil, errors.New("private Gmail relay unavailable")
		}
		proxy = "http://127.0.0.1:" + strconv.Itoa(port)
	}
	accounts := data["accounts"].([]any)
	if len(accounts) > 10 {
		return nil, errors.New("invalid mail configuration")
	}
	if len(accounts) >= 10 && in.ID == "" {
		return nil, errors.New("mail account limit reached")
	}
	var target map[string]any
	seen := map[string]bool{}
	for _, item := range accounts {
		a, ok := item.(map[string]any)
		if !ok {
			return nil, errors.New("invalid mail configuration")
		}
		id, _ := a["id"].(string)
		address, _ := a["address"].(string)
		provider, _ := a["provider"].(string)
		if id == "" || seen[id] || address == "" || (provider != "qq" && provider != "gmail") {
			return nil, errors.New("invalid mail configuration")
		}
		seen[id] = true
		if in.ID != "" && id == in.ID {
			if provider != in.Provider || !strings.EqualFold(address, in.Address) {
				return nil, errors.New("existing account identity cannot be changed")
			}
			target = a
		} else if strings.EqualFold(address, in.Address) {
			return nil, errors.New("mail account already exists")
		}
	}
	if in.ID != "" && target == nil {
		return nil, errors.New("mail account not found")
	}
	if target == nil {
		var id [16]byte
		if _, err = rand.Read(id[:]); err != nil {
			return nil, err
		}
		in.ID = "mail-" + hex.EncodeToString(id[:])
		if seen[in.ID] {
			return nil, errors.New("mail identity collision")
		}
		target = map[string]any{"id": in.ID, "address": in.Address, "provider": in.Provider}
		accounts = append(accounts, target)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]{1,80}$`).MatchString(in.ID) {
		return nil, errors.New("invalid mail account identity")
	}
	directory, err := privateMailDir()
	if err != nil {
		return nil, err
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return nil, err
	}
	credentialPath := filepath.Join(directory, hex.EncodeToString(random[:])+".json")
	bytes, err := json.Marshal(credential)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(credentialPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	if _, err = f.Write(bytes); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	target["credential_file"] = credentialPath
	if proxy != "" {
		target["proxy"] = proxy
	} else {
		delete(target, "proxy")
	}
	data["accounts"] = accounts
	out, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	if err = atomicWrite(filepath.Join(home, "assistant", "mail.json"), out); err != nil {
		return nil, err
	}
	return b.mailAccounts()
}
