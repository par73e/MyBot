package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

type instanceIdentity struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

type identityUpdate struct {
	Name     string `json:"name"`
	Address  string `json:"address"`
	Revision string `json:"revision"`
}

func identityFile() string { return filepath.Join(home, "assistant", "profile.json") }
func validIdentityText(v string, required bool) bool {
	if required && strings.TrimSpace(v) == "" {
		return false
	}
	if utf8.RuneCountInString(v) > 32 {
		return false
	}
	for _, ch := range v {
		if unicode.IsControl(ch) {
			return false
		}
	}
	return true
}
func (b *bridge) identity() (map[string]any, error) {
	if !isolatedRuntime {
		return nil, errors.New("instance identity unavailable")
	}
	raw, err := os.ReadFile(identityFile())
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if len(raw) > 4096 {
		return nil, errors.New("identity too large")
	}
	var value instanceIdentity
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
	}
	return map[string]any{"name": value.Name, "address": value.Address, "revision": hash(raw)}, nil
}
func (b *bridge) updateIdentity(in identityUpdate) (map[string]any, error) {
	b.profileMu.Lock()
	defer b.profileMu.Unlock()
	current, err := b.identity()
	if err != nil {
		return nil, err
	}
	if in.Revision != current["revision"] {
		return nil, errors.New("identity revision changed")
	}
	in.Name, in.Address = strings.TrimSpace(in.Name), strings.TrimSpace(in.Address)
	if !validIdentityText(in.Name, true) || !validIdentityText(in.Address, false) {
		return nil, errors.New("invalid identity")
	}
	raw, err := json.Marshal(instanceIdentity{Name: in.Name, Address: in.Address})
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(identityFile()), 0700); err != nil {
		return nil, err
	}
	if err = atomicWrite(identityFile(), raw); err != nil {
		return nil, err
	}
	return b.identity()
}
