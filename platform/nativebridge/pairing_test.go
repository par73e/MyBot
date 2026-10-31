package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstancePairingIsScopedIdempotentAndPrivate(t *testing.T) {
	oldHome, oldMode := home, isolatedRuntime
	t.Cleanup(func() { home, isolatedRuntime = oldHome, oldMode })
	home, isolatedRuntime = t.TempDir(), true
	t.Setenv("REASONIX_HOME", home)
	config := "default_model = \"demo/demo\"\n[bot]\nenabled = false\n[bot.allowlist]\nenabled = true\nallow_all = false\nweixin_users = []\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	b := &bridge{}
	input := instancePairing{Account: "synthetic-bot", User: "synthetic-user", Token: strings.Repeat("x", 40), Base: "https://ilinkai.weixin.qq.com"}
	if _, err := b.pairInstance(input); err != nil {
		t.Fatal(err)
	}
	if _, err := b.pairInstance(input); err != nil {
		t.Fatalf("retry must be idempotent: %v", err)
	}
	path := filepath.Join(home, "weixin", "accounts", "synthetic-bot.json")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("credential mode invalid")
	}
	configRaw, _ := os.ReadFile(filepath.Join(home, "config.toml"))
	if strings.Contains(string(configRaw), input.Token) {
		t.Fatal("credential leaked into config")
	}
	ownership, err := b.instanceOwnership()
	if err != nil || ownership["account"] != input.Account || ownership["credential_configured"] != true || len(ownership) != 2 {
		t.Fatalf("unexpected ownership attestation: %v %v", ownership, err)
	}
	t.Setenv("WEIXIN_BOT_TOKEN", "different-synthetic-token")
	if _, err = b.instanceOwnership(); err == nil {
		t.Fatal("credential override was not rejected")
	}
	input.User = "other-user"
	if _, err := b.pairInstance(input); err == nil {
		t.Fatal("owner replacement accepted")
	}
}
func TestInstancePairingRejectsUnsafeSources(t *testing.T) {
	oldHome, oldMode := home, isolatedRuntime
	t.Cleanup(func() { home, isolatedRuntime = oldHome, oldMode })
	home, isolatedRuntime = t.TempDir(), true
	b := &bridge{}
	for _, in := range []instancePairing{
		{Account: "../bad", User: "u", Token: "token", Base: "https://ilinkai.weixin.qq.com"},
		{Account: "good", User: "u", Token: "token", Base: "https://evil.example"},
		{Account: "good", User: "u", Token: "token", Base: "https://ilinkai.weixin.qq.com.evil.example"},
	} {
		if _, err := b.pairInstance(in); err == nil {
			t.Fatal("unsafe pairing accepted")
		}
	}
}
