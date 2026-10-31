package main

import (
	"errors"
	"os"
	"path/filepath"
)

type githubCredentialInput struct {
	Revision string `json:"revision"`
	Token    string `json:"token"`
}

func (b *bridge) githubCredentialStatus() (map[string]any, error) {
	if !isolatedRuntime {
		return nil, errors.New("GitHub credential setup unavailable")
	}
	dir := filepath.Join(home, "credentials")
	if info, err := os.Lstat(dir); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
			return nil, errors.New("GitHub credential directory is not private")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	path := filepath.Join(dir, "github-pr-monitor.token")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return map[string]any{"configured": false, "revision": hash(nil)}, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4096 {
		return nil, errors.New("GitHub credential unavailable")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return map[string]any{"configured": len(raw) > 0, "revision": hash(raw)}, nil
}

func (b *bridge) updateGitHubCredential(in githubCredentialInput) (map[string]any, error) {
	if !isolatedRuntime {
		return nil, errors.New("GitHub credential setup unavailable")
	}
	b.profileMu.Lock()
	defer b.profileMu.Unlock()
	status, err := b.githubCredentialStatus()
	if err != nil {
		return nil, err
	}
	if status["revision"] != in.Revision {
		return nil, errors.New("GitHub credential revision changed; refresh first")
	}
	if !validMailSecret(in.Token, true) || len(in.Token) > 4096 {
		return nil, errors.New("invalid GitHub credential")
	}
	dir := filepath.Join(home, "credentials")
	if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("GitHub credential directory is not private")
	}
	if err = atomicWrite(filepath.Join(dir, "github-pr-monitor.token"), []byte(in.Token)); err != nil {
		return nil, err
	}
	return b.githubCredentialStatus()
}
