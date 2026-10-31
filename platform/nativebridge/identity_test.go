package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstanceIdentityIsPrivateAndRevisionChecked(t *testing.T) {
	oldHome, oldMode := home, isolatedRuntime
	t.Cleanup(func() { home, isolatedRuntime = oldHome, oldMode })
	home, isolatedRuntime = t.TempDir(), true
	b := &bridge{}
	initial, err := b.identity()
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.updateIdentity(identityUpdate{Name: "助手甲", Address: "小林", Revision: initial["revision"].(string)})
	if err != nil {
		t.Fatal(err)
	}
	if got["name"] != "助手甲" || got["address"] != "小林" {
		t.Fatal("identity not saved")
	}
	info, err := os.Stat(filepath.Join(home, "assistant", "profile.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("identity permissions invalid")
	}
	if _, err := b.updateIdentity(identityUpdate{Name: "other", Revision: initial["revision"].(string)}); err == nil {
		t.Fatal("stale update accepted")
	}
	if _, err := b.updateIdentity(identityUpdate{Name: "bad\nname", Revision: got["revision"].(string)}); err == nil {
		t.Fatal("control character accepted")
	}
	isolatedRuntime = false
	if _, err := b.identity(); err == nil {
		t.Fatal("legacy identity unexpectedly changed")
	}
}
