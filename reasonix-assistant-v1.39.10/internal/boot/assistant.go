package boot

import (
	"reasonix/internal/assistant"
	"reasonix/internal/config"
	"reasonix/internal/heartbeat"
	"reasonix/internal/session"
	"reasonix/internal/tool"
)

func registerPersonalAssistant(reg *tool.Registry, cfg *config.Config, root string, service *session.Service) {
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
	if options.Target != nil {
		var query *session.Query
		if service != nil {
			query = service.Query()
		}
		dir := config.ProjectSessionDir(root)
		if dir == "" {
			dir = config.SessionDir()
		}
		reg.Add(assistant.HistoryTool{Repository: assistant.NewRepository(config.MemoryUserDir()), Owner: assistant.ConversationOwner(options.Target.ConnectionID, options.Target.ChatID, root), Root: session.RootForLegacyDir(dir), Query: query, LegacySessionID: assistant.LegacyConversationID(options.Target.ConnectionID, options.Target.ChatID)})
	}
}

// The opt-in assistant has a stable, directly advertised personal state contract.
func personalAssistantSurface(reg *tool.Registry, allow []string) []string {
	if !assistant.Enabled() {
		return allow
	}
	allow = nil
	// Technical tools remain registered and callable through use_capability;
	// their schemas need not frame every ordinary assistant conversation.
	for _, name := range []string{"assistant_tasks", "assistant_memory", "assistant_history", "ask", "web_search", "view_image", "compress", "use_capability", "create_goal", "get_goal", "update_goal"} {
		if _, ok := reg.Get(name); ok {
			allow = append(allow, name)
		}
	}
	return allow
}
