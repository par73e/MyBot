package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitHubCredentialIsPrivateAndRevisionChecked(t *testing.T) {
	oldHome, oldMode := home, isolatedRuntime
	t.Cleanup(func() { home, isolatedRuntime = oldHome, oldMode })
	home, isolatedRuntime = t.TempDir(), true
	b := &bridge{}
	initial, err := b.githubCredentialStatus()
	if err != nil || initial["configured"] != false {
		t.Fatal("new account was not empty")
	}
	in := githubCredentialInput{Revision: initial["revision"].(string), Token: "synthetic-private-token"}
	result, err := b.updateGitHubCredential(in)
	if err != nil || result["configured"] != true {
		t.Fatalf("credential save failed: %v", err)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), in.Token) {
		t.Fatal("token leaked into response")
	}
	path := filepath.Join(home, "credentials", "github-pr-monitor.token")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("token file is not private")
	}
	if _, err = b.updateGitHubCredential(in); err == nil {
		t.Fatal("stale revision accepted")
	}
	if content, _ := os.ReadFile(path); string(content) != in.Token {
		t.Fatal("token changed after stale request")
	}
}
