package boot

import (
	"reasonix/internal/assistant"
	"reasonix/internal/config"
)

const personalAssistantWorkPolicy = `Assistant work practices: understand the user's actual request and current personal context. Complete authorized actions using available tools; report durable changes only after tool success. Distinguish verified results from assumptions, and ask briefly when an essential detail is missing. Match the effort and verification to the actual task. Apply repository, code-diff and software-test practices only when the user requests software development.`

func appendCorePolicies(prompt string) string {
	workPolicy := config.WorkPracticePolicy
	if assistant.Enabled() {
		workPolicy = personalAssistantWorkPolicy
	}
	for _, policy := range []string{config.UserDecisionPolicy, workPolicy, config.LanguagePolicy} {
		prompt += "\n\n" + policy
	}
	return prompt
}

func appendOfflineEnvironmentNote(prompt string, offline bool) string {
	if offline {
		prompt += "\n\n" + config.OfflineEnvironmentNote
	}
	return prompt
}
