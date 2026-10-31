package main

import (
	"strings"
	"testing"
)

func TestIsolatedRuntimeRequiresCompleteOperatorConfiguration(t *testing.T) {
	oldHome, oldWorkspace, oldState, oldToken, oldSocket, oldExe, oldUnit := home, workspace, state, tokenPath, socketPath, executablePath, serviceUnit
	oldIsolated := isolatedRuntime
	oldMailcheck := mailcheckPath
	t.Cleanup(func() {
		home, workspace, state, tokenPath, socketPath, executablePath, serviceUnit = oldHome, oldWorkspace, oldState, oldToken, oldSocket, oldExe, oldUnit
		isolatedRuntime = oldIsolated
		mailcheckPath = oldMailcheck
	})
	root := "/isolated/" + strings.Repeat("a", 32)
	env := map[string]string{
		"ASROBOT_INSTANCE_MODE": "isolated",
		"ASROBOT_RUNTIME_ROOT":  root,
		"ASROBOT_RUNTIME_HOME":  root + "/home", "ASROBOT_RUNTIME_WORKSPACE": root + "/workspace",
		"ASROBOT_RUNTIME_STATE": root + "/state", "ASROBOT_RUNTIME_TOKEN": root + "/token",
		"ASROBOT_RUNTIME_SOCKET": root + "/socket", "ASROBOT_RUNTIME_EXECUTABLE": "/isolated/reasonix",
		"ASROBOT_RUNTIME_UNIT": "asrobot-instance-" + strings.Repeat("a", 32) + ".service",
	}
	get := func(key string) string { return env[key] }
	env["ASROBOT_RUNTIME_UNIT"] = "mybot-heartbeat.service"
	if configureRuntime(get) == nil {
		t.Fatal("isolated instance accepted original owner's service")
	}
	if home != oldHome || serviceUnit != oldUnit {
		t.Fatal("invalid settings partially applied")
	}
	env["ASROBOT_RUNTIME_UNIT"] = "asrobot-instance-" + strings.Repeat("a", 32) + ".service"
	env["ASROBOT_RUNTIME_HOME"] = "/isolated/../other"
	if configureRuntime(get) == nil {
		t.Fatal("unclean path accepted")
	}
	env["ASROBOT_RUNTIME_HOME"] = "/other/home"
	if configureRuntime(get) == nil {
		t.Fatal("path outside instance root accepted")
	}
	env["ASROBOT_RUNTIME_HOME"] = root + "/home"
	if err := configureRuntime(get); err != nil {
		t.Fatal(err)
	}
	if home != root+"/home" || serviceUnit != env["ASROBOT_RUNTIME_UNIT"] {
		t.Fatal("instance configuration not applied")
	}
}
