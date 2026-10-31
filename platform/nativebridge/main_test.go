package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/BurntSushi/toml"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNestedCredentialsNeverReachJSON(t *testing.T) {
	value := map[string]any{"providers": []map[string]any{{"api_key": "private-api-value", "api_key_env": "KEY_NAME"}}, "accounts": []any{map[string]any{"credential_file": "/private/key.json", "address": "owner@example.test", "token": map[string]any{"access_token": "private-token"}}}}
	raw, _ := json.Marshal(public(value))
	text := string(raw)
	for _, secret := range []string{"private-api-value", "/private/key.json", "private-token"} {
		if strings.Contains(text, secret) {
			t.Fatal("credential leaked")
		}
	}
	if !strings.Contains(text, "owner@example.test") || !strings.Contains(text, "KEY_NAME") {
		t.Fatal("useful owner configuration hidden")
	}
}

func TestCustomCredentialHeaderNamesNeverReachJSON(t *testing.T) {
	value := map[string]any{"headers": map[string]any{"Authorization": "private-bearer-value", "X-Api-Key": "private-header-key", "Cookie": "private-cookie-value"}, "QWEN_API_KEY": "private-environment-value", "api_key_env": "ORIGINAL_KEY_NAME"}
	raw, _ := json.Marshal(public(value))
	for _, private := range []string{"private-bearer-value", "private-header-key", "private-cookie-value", "private-environment-value"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("custom credential field leaked")
		}
	}
	if !strings.Contains(string(raw), "ORIGINAL_KEY_NAME") {
		t.Fatal("environment variable reference was hidden")
	}
}

func TestModelChangesPreserveUnrelatedOriginalConfiguration(t *testing.T) {
	cfg := map[string]any{}
	text := `default_model="mine/old"
[agent]
system_prompt_file="/private/original.md"
[bot]
model="mine/old"
[bot.allowlist]
weixin_users=["owner"]
[[providers]]
name="mine"
kind="openai"
model="old"
api_key_env="ORIGINAL_KEY"
base_url="https://original.example/v1"
default_effort="medium"
[[providers]]
name="other"
model="untouched"
base_url="https://other.example/v1"
`
	if _, e := toml.Decode(text, &cfg); e != nil {
		t.Fatal(e)
	}
	env := map[string]string{"ORIGINAL_KEY": "old-private", "OTHER_KEY": "unchanged"}
	in := map[string]json.RawMessage{"model_ref": json.RawMessage(`"mine/new"`), "reasoning_effort": json.RawMessage(`"low"`), "api_key": json.RawMessage(`"new-private"`)}
	if e := changeModel(cfg, env, in); e != nil {
		t.Fatal(e)
	}
	var out bytes.Buffer
	if e := toml.NewEncoder(&out).Encode(cfg); e != nil {
		t.Fatal(e)
	}
	got := map[string]any{}
	if _, e := toml.Decode(out.String(), &got); e != nil {
		t.Fatal(e)
	}
	if got["default_model"] != "mine/new" || got["bot"].(map[string]any)["model"] != "mine/new" {
		t.Fatal("model routes diverged")
	}
	providers := got["providers"].([]map[string]any)
	if providers[1]["model"] != "untouched" || providers[0]["base_url"] != "https://original.example/v1" || env["OTHER_KEY"] != "unchanged" {
		t.Fatal("unrequested configuration changed")
	}
	if got["agent"].(map[string]any)["system_prompt_file"] != "/private/original.md" {
		t.Fatal("original prompt path changed")
	}
}
func TestPausedOwnerCannotStartQueuedNativeChat(t *testing.T) {
	b := &bridge{paused: true}
	if _, e := b.chat(context.Background(), "do not start"); e == nil {
		t.Fatal("paused chat started")
	}
}
func TestHTTPRequiresPrivateTokenAndFixedSource(t *testing.T) {
	b := &bridge{token: strings.Repeat("x", 48)}
	for _, tc := range []struct {
		path, auth string
		code       int
	}{{"/v1/status?source=friday_full", "", 403}, {"/v1/status?source=someone_else", "Bearer " + b.token, 404}} {
		r := httptest.NewRequest("GET", tc.path, nil)
		r.Header.Set("Authorization", tc.auth)
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatalf("code %d", w.Code)
		}
	}
}
func TestAtomicWritePreservesPermissionsAndFullText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prompt.md")
	body := []byte(strings.Repeat("原样保留\n", 1000))
	if e := atomicWrite(path, body); e != nil {
		t.Fatal(e)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(body) {
		t.Fatal("content changed")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("unsafe permissions")
	}
}
func TestCappedOutputRejectsExcess(t *testing.T) {
	w := &cappedBuffer{limit: 4}
	if _, e := w.Write([]byte("1234")); e != nil {
		t.Fatal(e)
	}
	if _, e := w.Write([]byte("x")); e == nil {
		t.Fatal("unbounded output")
	}
}

func TestUserPromptUsesTheSameHomeFallbackAsNativeSDK(t *testing.T) {
	root := t.TempDir()
	userHome := t.TempDir()
	if e := os.Mkdir(filepath.Join(userHome, "prompts"), 0700); e != nil {
		t.Fatal(e)
	}
	expected := filepath.Join(userHome, "prompts", "friday.md")
	if e := os.WriteFile(expected, []byte("full original prompt"), 0600); e != nil {
		t.Fatal(e)
	}
	got, e := resolvePromptPath("prompts/friday.md", root, userHome)
	if e != nil || got != expected {
		t.Fatalf("wrong prompt path %s %v", got, e)
	}
}

func TestOutputCopyCannotBypassLimitThroughEmbeddedBuffer(t *testing.T) {
	w := &cappedBuffer{limit: 4}
	if _, e := w.ReadFrom(strings.NewReader("12345")); e == nil {
		t.Fatal("ReaderFrom bypassed output limit")
	}
}

func TestConfiguredURLsDoNotExposeProxyOrQueryCredentials(t *testing.T) {
	raw, _ := json.Marshal(public(map[string]any{"proxy": "https://name:private-password@example.test", "balance_url": "https://example.test/status?api_key=private-query&plan=normal"}))
	if strings.Contains(string(raw), "private-password") || strings.Contains(string(raw), "private-query") {
		t.Fatal("URL credential leaked")
	}
	if !strings.Contains(string(raw), "example.test") {
		t.Fatal("useful endpoint hidden")
	}
}

func TestEmptySecretStoreIsNotReportedAsConfigured(t *testing.T) {
	got := public(map[string]any{"secrets": map[string]any{}}).(map[string]any)
	if got["secrets_configured"] != false {
		t.Fatal("empty secret store reported as configured")
	}
}
