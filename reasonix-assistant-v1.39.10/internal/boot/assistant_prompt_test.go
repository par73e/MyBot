package boot

import (
	"strings"
	"testing"

	"reasonix/internal/config"
)

func TestPersonalAssistantWorkPolicyIsScoped(t *testing.T) {
	for _, enabled := range []string{"", "1"} {
		t.Run("assistant="+enabled, func(t *testing.T) {
			t.Setenv("REASONIX_ASSISTANT", enabled)
			prompt := appendCorePolicies("Friday")
			if !strings.Contains(prompt, config.UserDecisionPolicy) || !strings.Contains(prompt, config.LanguagePolicy) {
				t.Fatal("shared policies were lost")
			}
			if got := strings.Contains(prompt, config.WorkPracticePolicy); got != (enabled == "") {
				t.Fatal("coding policy applied outside its mode")
			}
			if got := strings.Contains(prompt, personalAssistantWorkPolicy); got != (enabled == "1") {
				t.Fatal("assistant policy applied outside its mode")
			}
		})
	}
}
