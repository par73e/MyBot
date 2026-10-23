package boot

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/config"
	"reasonix/internal/sessioncontext"
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

func TestPersonalAssistantProviderContextOmitsDeployment(t *testing.T) {
	for _, enabled := range []string{"", "1"} {
		t.Run("assistant="+enabled, func(t *testing.T) {
			t.Setenv("REASONIX_ASSISTANT", enabled)
			isolateConfigHome(t)
			dir := robustTempDir(t)
			t.Chdir(dir)
			registerBootTokenProfileTestProvider()
			prov := testutil.NewMock("assistant-context", testutil.Turn{Text: "hello"})
			setBootTokenProfileTestProvider(t, prov)
			writeFile(t, dir, "reasonix.toml", `
default_model = "test-model"
[agent]
system_prompt = "Friday"
[environment]
enabled = false
offline = true
[[providers]]
name = "test-model"
kind = "boot-token-profile-test"
model = "x"
`)
			approveWorkspace(t, dir)
			ctrl, err := Build(context.Background(), Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer ctrl.Close()
			if err := ctrl.Run(t.Context(), "hello"); err != nil {
				t.Fatal(err)
			}
			request := prov.LastRequest()
			if request == nil {
				t.Fatal("provider received no request")
			}
			snapshot, ok := sessioncontext.Parse(sessionContextMessage(request.Messages))
			if !ok {
				t.Fatal("provider received no valid current context")
			}
			if enabled == "1" {
				if snapshot.Sections.Environment != "" || snapshot.Sections.Workspace != "" || !strings.Contains(snapshot.Sections.BackgroundMemory, "personal_assistant_current_state") {
					t.Fatal("assistant context lost personal state or advertised deployment")
				}
				for _, schema := range request.Tools {
					if schema.Name == "bash" || schema.Name == "read_file" || schema.Name == "write_file" || schema.Name == "edit_file" {
						t.Fatalf("technical tool advertised in daily surface: %s", schema.Name)
					}
				}
			} else if snapshot.Sections.Workspace == "" || !strings.Contains(snapshot.Sections.Environment, config.OfflineEnvironmentNote) {
				t.Fatal("ordinary coding context lost deployment information")
			}
		})
	}
}
