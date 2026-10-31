package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/assistant"
)

func TestInstanceMailAccountsArePrivateAndRevisionChecked(t *testing.T) {
	oldHome, oldMode := home, isolatedRuntime
	t.Cleanup(func() { home, isolatedRuntime = oldHome, oldMode })
	home, isolatedRuntime = t.TempDir(), true
	b := &bridge{}
	initial, err := b.mailAccounts()
	if err != nil || len(initial["accounts"].([]map[string]any)) != 0 {
		t.Fatalf("initial accounts: %v %v", initial, err)
	}
	in := mailAccountInput{Revision: initial["revision"].(string), Provider: "qq", Address: "sample@qq.com", AuthorizationCode: "synthetic-private-code"}
	result, err := b.updateMailAccount(in)
	if err != nil {
		t.Fatal(err)
	}
	if configured, err := assistant.NewMailService(home).Config(); err != nil || len(configured.Accounts) != 1 {
		t.Fatalf("native mail service cannot read new account: %v", err)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), in.AuthorizationCode) || strings.Contains(string(raw), "credential_file") {
		t.Fatal("mail credential or path leaked into response")
	}
	config, _ := os.ReadFile(filepath.Join(home, "assistant", "mail.json"))
	if strings.Contains(string(config), in.AuthorizationCode) {
		t.Fatal("mail credential leaked into account configuration")
	}
	files, err := os.ReadDir(filepath.Join(home, "assistant", "mail-credentials"))
	if err != nil || len(files) != 1 {
		t.Fatal("expected one private credential")
	}
	info, err := files[0].Info()
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("credential mode is not private")
	}
	if _, err := b.updateMailAccount(in); err == nil {
		t.Fatal("stale revision accepted")
	}
	accounts := result["accounts"].([]map[string]any)
	in.ID = accounts[0]["id"].(string)
	in.Revision = result["revision"].(string)
	in.AuthorizationCode = "replacement-code"
	if _, err := b.updateMailAccount(in); err != nil {
		t.Fatal(err)
	}
	if old, _ := os.ReadFile(filepath.Join(home, "assistant", "mail-credentials", files[0].Name())); !strings.Contains(string(old), "synthetic-private-code") {
		t.Fatal("existing credential was silently overwritten")
	}
}

func TestInstanceGmailSetupRequiresItsOwnRelayWhenRequested(t *testing.T) {
	oldHome, oldMode := home, isolatedRuntime
	t.Cleanup(func() { home, isolatedRuntime = oldHome, oldMode })
	home, isolatedRuntime = t.TempDir(), true
	b := &bridge{}
	initial, err := b.mailAccounts()
	if err != nil {
		t.Fatal(err)
	}
	in := mailAccountInput{Revision: initial["revision"].(string), Provider: "gmail", Address: "sample@example.test", UseProxy: true,
		Gmail: gmailCredentialInput{ClientID: "client", ClientSecret: "secret", RefreshToken: "refresh"}}
	t.Setenv("ASROBOT_MAIL_PROXY_PORT", "")
	if _, err := b.updateMailAccount(in); err == nil {
		t.Fatal("missing relay accepted")
	}
	t.Setenv("ASROBOT_MAIL_PROXY_PORT", "17890")
	root, err := os.MkdirTemp("/tmp", "asr-mail-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	if err := os.Mkdir(filepath.Join(root, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(root, "state", "mail-proxy.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv("ASROBOT_RUNTIME_ROOT", root)
	t.Setenv("ASROBOT_RUNTIME_STATE", filepath.Join(root, "state"))
	t.Setenv("ASROBOT_MAIL_PROXY_SOCKET", socketPath)
	result, err := b.updateMailAccount(in)
	if err != nil || result["proxy_available"] != true {
		t.Fatalf("Gmail setup failed: %v", err)
	}
	data, _, err := readMailConfiguration()
	if err != nil || data["accounts"].([]any)[0].(map[string]any)["proxy"] != "http://127.0.0.1:17890" {
		t.Fatal("instance proxy was not configured")
	}
}
