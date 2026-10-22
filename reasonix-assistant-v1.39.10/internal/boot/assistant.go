package boot

import (
	"reasonix/internal/assistant"
	"reasonix/internal/config"
	"reasonix/internal/heartbeat"
	"reasonix/internal/tool"
)

func registerPersonalAssistant(reg *tool.Registry, cfg *config.Config, root string) {
	if !assistant.Enabled() {
		return
	}
	reg.Remove("remember")
	reg.Remove("forget")
	options := assistant.Options{Workspace: root, Model: cfg.DefaultModel}
	if len(cfg.Bot.Allowlist.WeixinUsers) == 1 {
		options.Target = &heartbeat.Target{ConnectionID: "weixin", Domain: "weixin", ChatID: cfg.Bot.Allowlist.WeixinUsers[0], ChatType: "dm"}
	}
	for _, t := range assistant.Tools(config.MemoryUserDir(), options) {
		reg.Add(t)
	}
}

// The opt-in assistant has a stable, directly advertised personal state contract.
func personalAssistantSurface(reg *tool.Registry, allow []string) []string {
	if !assistant.Enabled() {
		return allow
	}
	for _, name := range []string{"assistant_tasks", "assistant_memory"} {
		if _, ok := reg.Get(name); ok {
			allow = append(allow, name)
		}
	}
	return allow
}
