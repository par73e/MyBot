package assistant

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddressPrefixIsInstanceSpecificAndLegacyCompatible(t *testing.T) {
	oldAddress, hadAddress := os.LookupEnv("REASONIX_ASSISTANT_ADDRESS")
	oldMode, hadMode := os.LookupEnv("ASROBOT_INSTANCE_MODE")
	t.Cleanup(func() {
		if hadAddress {
			_ = os.Setenv("REASONIX_ASSISTANT_ADDRESS", oldAddress)
		} else {
			_ = os.Unsetenv("REASONIX_ASSISTANT_ADDRESS")
		}
		if hadMode {
			_ = os.Setenv("ASROBOT_INSTANCE_MODE", oldMode)
		} else {
			_ = os.Unsetenv("ASROBOT_INSTANCE_MODE")
		}
	})
	_ = os.Unsetenv("REASONIX_ASSISTANT_ADDRESS")
	_ = os.Unsetenv("ASROBOT_INSTANCE_MODE")
	if got := addressPrefix(); got != "Sir，" {
		t.Fatalf("legacy address = %q", got)
	}
	if got := neutralInstanceInstructions("Friday serves Sir"); got != "Friday serves Sir" {
		t.Fatalf("legacy prompt changed: %q", got)
	}
	t.Setenv("ASROBOT_INSTANCE_MODE", "isolated")
	if got := neutralInstanceInstructions("Friday serves Sir"); got != "助手 serves 用户" {
		t.Fatalf("instance prompt = %q", got)
	}
	dir := t.TempDir()
	t.Setenv("REASONIX_HOME", dir)
	if got := addressPrefix(); got != "" {
		t.Fatalf("new instance inherited an address: %q", got)
	}
	if err := os.Mkdir(filepath.Join(dir, "assistant"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assistant", "profile.json"), []byte(`{"name":"助手甲","address":"小林"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := addressPrefix(); got != "小林，" {
		t.Fatalf("stored address = %q", got)
	}
	if got := MailHealthMessageForInstance(); !strings.HasPrefix(got, "小林，") || strings.Contains(got, "Sir") {
		t.Fatalf("instance mail health message = %q", got)
	}
	for _, description := range []string{Tools(dir, Options{})[0].Description(), AlertTool{}.Description(), SubscriptionTool{}.Description(), HeartbeatTool{}.Description(), MailScheduleTool{}.Description(), ProposalTool{}.Description(), MorningTool{}.Description()} {
		if strings.Contains(description, "Sir") || strings.Contains(description, "Friday") {
			t.Fatalf("instance tool description retains legacy name: %s", description)
		}
	}
	for _, module := range HeartbeatModules() {
		if strings.Contains(module.Name, "Friday") {
			t.Fatalf("instance module retains legacy name: %q", module.Name)
		}
	}
	t.Setenv("REASONIX_ASSISTANT_ADDRESS", "小林")
	if got := addressPrefix(); got != "小林，" {
		t.Fatalf("custom address = %q", got)
	}
	t.Setenv("REASONIX_ASSISTANT_ADDRESS", "bad\naddress")
	if got := addressPrefix(); got != "" {
		t.Fatalf("unsafe address = %q", got)
	}
}
