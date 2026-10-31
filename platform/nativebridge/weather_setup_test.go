package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/assistant"
)

func TestWeatherSetupCreatesPrivateOwnerConfigurationWithoutOverwrite(t *testing.T) {
	oldHome, oldMode := home, isolatedRuntime
	t.Cleanup(func() { home, isolatedRuntime = oldHome, oldMode })
	home, isolatedRuntime = t.TempDir(), true
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	private := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	b := &bridge{}
	status, err := b.weatherSetupStatus()
	if err != nil || status["configured"] != false {
		t.Fatal("new instance was not empty")
	}
	in := weatherSetupInput{APIHost: "example.example.qweatherapi.com", DeveloperID: "developer", ProjectID: "project", CredentialID: "key", PrivateKey: private, LocationName: "Sample", Latitude: 31.2, Longitude: 121.5, WakeTime: "08:00"}
	result, err := b.configureWeather(in)
	if err != nil || result["configured"] != true {
		t.Fatalf("weather setup failed: %v", err)
	}
	response, _ := json.Marshal(result)
	if strings.Contains(string(response), private) || strings.Contains(string(response), "private_key") {
		t.Fatal("private key leaked")
	}
	config, err := assistant.NewWeatherService(home).Config()
	if err != nil || config.MorningEnabled {
		t.Fatalf("native weather configuration invalid: %v", err)
	}
	var credentials struct {
		Private string `json:"private_key_file"`
	}
	raw, err := os.ReadFile(config.CredentialFile)
	if err != nil || json.Unmarshal(raw, &credentials) != nil {
		t.Fatal("credential file unavailable")
	}
	info, err := os.Stat(credentials.Private)
	if err != nil || info.Mode().Perm() != 0600 || !strings.HasPrefix(credentials.Private, filepath.Join(home, "assistant")+string(os.PathSeparator)) {
		t.Fatal("weather key is not private")
	}
	before, _ := os.ReadFile(filepath.Join(home, "assistant", "weather.json"))
	if _, err = b.configureWeather(in); err == nil {
		t.Fatal("existing weather configuration was overwritten")
	}
	after, _ := os.ReadFile(filepath.Join(home, "assistant", "weather.json"))
	if string(before) != string(after) {
		t.Fatal("existing weather configuration changed")
	}
}
