package boot

import (
	"reasonix/internal/assistant"
	"reasonix/internal/tool"
	"slices"
	"testing"
)

func TestLocationVisibleInForegroundButUnavailableToMailAnalysis(t *testing.T) {
	t.Setenv("REASONIX_ASSISTANT", "1")
	reg := tool.NewRegistry()
	reg.Add(assistant.LocationTool{Service: assistant.LocationService{Home: t.TempDir()}})
	if !slices.Contains(personalAssistantSurface(reg, nil), "assistant_location") {
		t.Fatal("location tool not exposed to owner conversations")
	}
	t.Setenv("REASONIX_ASSISTANT_MAIL_BACKGROUND", "1")
	reg = tool.NewRegistry()
	reg.Add(assistant.LocationTool{})
	personalAssistantSurface(reg, nil)
	if _, ok := reg.Get("assistant_location"); ok {
		t.Fatal("mail analysis can access location changes")
	}
}
