package boot

import (
	"reasonix/internal/tool"
	"testing"
)

func TestWeatherBackgroundExposesOnlyReadOnlyPersonalTools(t *testing.T) {
	t.Setenv("REASONIX_ASSISTANT", "1")
	t.Setenv("REASONIX_ASSISTANT_WEATHER_BACKGROUND", "1")
	reg := tool.NewRegistry()
	for _, name := range []string{"assistant_weather", "assistant_history", "assistant_tasks", "assistant_memory", "assistant_mail", "assistant_mail_schedule", "bash", "use_capability", "web_search", "run_skill", "task"} {
		reg.Add(mailDummyTool(name))
	}
	visible := personalAssistantSurface(reg, nil)
	if len(visible) != 4 || reg.Len() != 4 {
		t.Fatal("weather background escaped four-tool scope", visible, reg.AllNames())
	}
	for _, name := range []string{"assistant_weather", "assistant_history", "assistant_tasks", "assistant_memory"} {
		if _, ok := reg.Get(name); !ok {
			t.Fatal("missing background tool", name)
		}
	}
}
