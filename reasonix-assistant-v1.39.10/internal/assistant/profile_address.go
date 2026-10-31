package assistant

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type personalProfile struct {
	Name    string `json:"assistant_name"`
	Address string `json:"user_address"`
}

// neutralInstanceInstructions adapts fixed built-in instructions, never user
// content, for instances whose owner and assistant choose their own names.
func neutralInstanceInstructions(text string) string {
	if os.Getenv("ASROBOT_INSTANCE_MODE") != "isolated" {
		return text
	}
	return strings.NewReplacer("Friday", "助手", "Sir's", "用户的", "Sir", "用户").Replace(text)
}

// NeutralInstanceInstructions adapts trusted built-in policies only; callers
// must never pass a user's message, memory, or customized prompt through it.
func NeutralInstanceInstructions(text string) string { return neutralInstanceInstructions(text) }

func loadPersonalProfile(home string) *personalProfile {
	if os.Getenv("ASROBOT_INSTANCE_MODE") != "isolated" || home == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(home, "assistant", "profile.json"))
	if err != nil || len(raw) > 4096 {
		return nil
	}
	var stored struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	}
	if json.Unmarshal(raw, &stored) != nil {
		return nil
	}
	if utf8.RuneCountInString(stored.Name) > 32 || utf8.RuneCountInString(stored.Address) > 32 || strings.ContainsAny(stored.Name+stored.Address, "\r\n\x00") {
		return nil
	}
	return &personalProfile{Name: stored.Name, Address: stored.Address}
}

// addressPrefix uses the instance's own trusted configuration. Existing
// single-user installations retain their historic greeting until migrated.
func addressPrefix() string {
	value, configured := os.LookupEnv("REASONIX_ASSISTANT_ADDRESS")
	if !configured {
		if os.Getenv("ASROBOT_INSTANCE_MODE") == "isolated" {
			profile := loadPersonalProfile(os.Getenv("REASONIX_HOME"))
			if profile == nil {
				return ""
			}
			value = profile.Address
		} else {
			value = "Sir"
		}
	}
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > 32 || strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	return value + "，"
}
