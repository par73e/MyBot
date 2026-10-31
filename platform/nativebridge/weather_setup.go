package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type weatherSetupInput struct {
	APIHost      string  `json:"api_host"`
	DeveloperID  string  `json:"developer_id"`
	ProjectID    string  `json:"project_id"`
	CredentialID string  `json:"credential_id"`
	PrivateKey   string  `json:"private_key"`
	LocationName string  `json:"location_name"`
	Latitude     float64 `json:"latitude"`
	Longitude    float64 `json:"longitude"`
	WakeTime     string  `json:"wake_time"`
}

func (b *bridge) weatherSetupStatus() (map[string]any, error) {
	if !isolatedRuntime {
		return nil, errors.New("weather setup unavailable")
	}
	parent := filepath.Join(home, "assistant")
	if info, err := os.Lstat(parent); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("weather configuration unavailable")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	path := filepath.Join(parent, "weather.json")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return map[string]any{"configured": false}, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("weather configuration unavailable")
	}
	return map[string]any{"configured": true}, nil
}

func writeExclusivePrivate(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func atomicWriteNew(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".weather-config-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Link(tmp, path)
}

func (b *bridge) configureWeather(in weatherSetupInput) (map[string]any, error) {
	if !isolatedRuntime {
		return nil, errors.New("weather setup unavailable")
	}
	b.profileMu.Lock()
	defer b.profileMu.Unlock()
	current, err := b.weatherSetupStatus()
	if err != nil || current["configured"] != false {
		return nil, errors.New("existing weather configuration needs review")
	}
	validID := regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	validHost := regexp.MustCompile(`^[a-z0-9-]+\.[a-z0-9-]+\.qweatherapi\.com$`)
	validClock := regexp.MustCompile(`^(?:[01][0-9]|2[0-3]):[0-5][0-9]$`)
	in.LocationName = strings.TrimSpace(in.LocationName)
	if !validHost.MatchString(in.APIHost) || !validID.MatchString(in.DeveloperID) || !validID.MatchString(in.ProjectID) || !validID.MatchString(in.CredentialID) ||
		!validMailSecret(in.LocationName, true) || len(in.LocationName) > 100 || !validClock.MatchString(in.WakeTime) ||
		math.IsNaN(in.Latitude) || math.IsInf(in.Latitude, 0) || in.Latitude < -90 || in.Latitude > 90 ||
		math.IsNaN(in.Longitude) || math.IsInf(in.Longitude, 0) || in.Longitude < -180 || in.Longitude > 180 || len(in.PrivateKey) > 16384 {
		return nil, errors.New("invalid weather configuration")
	}
	block, rest := pem.Decode([]byte(in.PrivateKey))
	if block == nil || block.Type != "PRIVATE KEY" || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, errors.New("weather requires an Ed25519 private key")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("weather requires an Ed25519 private key")
	}
	if _, ok := key.(ed25519.PrivateKey); !ok {
		return nil, errors.New("weather requires an Ed25519 private key")
	}
	parent := filepath.Join(home, "assistant")
	if err := os.MkdirAll(parent, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("weather credential directory is not private")
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return nil, err
	}
	dir := filepath.Join(parent, "weather-"+hex.EncodeToString(random[:]))
	if err = os.Mkdir(dir, 0700); err != nil {
		return nil, err
	}
	privatePath := filepath.Join(dir, "private.pem")
	credentialPath := filepath.Join(dir, "credentials.json")
	locationsPath := filepath.Join(dir, "locations.json")
	credentials, _ := json.Marshal(map[string]any{"api_host": in.APIHost, "developer_id": in.DeveloperID, "project_id": in.ProjectID, "credential_id": in.CredentialID, "private_key_file": privatePath})
	days := []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}
	defaults, wake := map[string]string{}, map[string]string{}
	for _, day := range days {
		defaults[day], wake[day] = "home", in.WakeTime
	}
	locations, _ := json.Marshal(map[string]any{"timezone": "Asia/Shanghai", "default_by_weekday": defaults, "places": map[string]any{"home": map[string]any{"name": in.LocationName, "latitude": in.Latitude, "longitude": in.Longitude}}})
	configuration, _ := json.Marshal(map[string]any{"enabled": true, "credential_file": credentialPath, "locations_file": locationsPath, "morning_enabled": false, "wake_times": wake})
	for path, data := range map[string][]byte{privatePath: []byte(in.PrivateKey), credentialPath: credentials, locationsPath: locations} {
		if err = writeExclusivePrivate(path, data); err != nil {
			return nil, err
		}
	}
	if err = atomicWriteNew(filepath.Join(parent, "weather.json"), configuration); err != nil {
		return nil, err
	}
	return map[string]any{"configured": true}, nil
}
