package cli

func botCompletionSpec(model, help cliCompletionFlag) cliCompletionSpec {
	return completionSpec("bot", []cliCompletionFlag{help},
		completionSpec("start", []cliCompletionFlag{
			completionFlag("--channels --dir", cliCompletionStaticValue), model, help,
			completionFlag("--heartbeat", cliCompletionNoValue),
			completionFlag("--heartbeat-file", cliCompletionPathValue),
			completionFlag("--heartbeat-poll", cliCompletionStaticValue),
		}),
		completionSpec("doctor", []cliCompletionFlag{completionFlag("--json --deep", cliCompletionNoValue), help}),
		completionSpec("pairing", []cliCompletionFlag{help},
			completionSpec("list", []cliCompletionFlag{help}),
			completionSpec("approve", []cliCompletionFlag{help}),
			completionSpecWithAliases("reject", []string{"deny"}, []cliCompletionFlag{help}),
		),
		completionSpec("weixin-login", []cliCompletionFlag{completionFlag("--timeout", cliCompletionStaticValue), help}),
	)
}

func heartbeatCompletionSpec(help cliCompletionFlag) cliCompletionSpec {
	file := completionFlag("--file", cliCompletionPathValue)
	id := completionFlag("--id", cliCompletionStaticValue)
	return completionSpec("heartbeat", []cliCompletionFlag{help},
		completionSpec("add", []cliCompletionFlag{
			file, help, completionFlag("--title --prompt --interval --at --timezone --ends-at --model --window-start --window-end --connection --chat --domain --chat-type", cliCompletionStaticValue),
			completionFlag("--kind", cliCompletionStaticValue, "agent", "message"),
			completionFlag("--permission-mode", cliCompletionStaticValue, "read-only", "workspace-write"),
			completionFlag("--dir", cliCompletionPathValue), completionFlag("--reuse-session", cliCompletionNoValue),
		}),
		completionSpec("list", []cliCompletionFlag{file, help}),
		completionSpec("enable", []cliCompletionFlag{file, id, help}),
		completionSpec("disable", []cliCompletionFlag{file, id, help}),
		completionSpec("remove", []cliCompletionFlag{file, id, help}),
		completionSpec("start", []cliCompletionFlag{file, help, completionFlag("--poll", cliCompletionStaticValue)}),
		completionSpec("tick", []cliCompletionFlag{file, help}),
		completionSpec("run", []cliCompletionFlag{file, id, help}),
	)
}
