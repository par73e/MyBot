package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Runtime settings come only from the operator-controlled service environment,
// never from a web request. Legacy defaults preserve the existing deployment.
var home = "/var/lib/mybot/reasonix"
var workspace = "/var/lib/mybot/workspace"
var state = "/var/lib/asrobot-native"
var tokenPath = "/etc/asrobot-native/token"
var socketPath = "/run/asrobot-native/native.sock"
var executablePath = "/opt/mybot/current/reasonix"
var mailcheckPath = "/opt/asrobot-native/mailcheck.py"
var serviceUnit = "mybot-heartbeat.service"
var isolatedRuntime bool

func configureRuntime(getenv func(string) string) error {
	if getenv("ASROBOT_INSTANCE_MODE") == "" {
		return nil
	}
	if getenv("ASROBOT_INSTANCE_MODE") != "isolated" {
		return errors.New("invalid instance mode")
	}
	paths := []*string{&home, &workspace, &state, &tokenPath, &socketPath, &executablePath}
	names := []string{"ASROBOT_RUNTIME_HOME", "ASROBOT_RUNTIME_WORKSPACE", "ASROBOT_RUNTIME_STATE", "ASROBOT_RUNTIME_TOKEN", "ASROBOT_RUNTIME_SOCKET", "ASROBOT_RUNTIME_EXECUTABLE"}
	values := make([]string, len(paths))
	for i, name := range names {
		value := getenv(name)
		if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value || value == "/" {
			return fmt.Errorf("invalid %s", name)
		}
		values[i] = value
	}
	unit := getenv("ASROBOT_RUNTIME_UNIT")
	if !regexp.MustCompile(`^asrobot-instance-[0-9a-f]{32}\.service$`).MatchString(unit) {
		return errors.New("invalid isolated runtime service")
	}
	mailcheck := getenv("ASROBOT_RUNTIME_MAILCHECK")
	if mailcheck != "" && (!filepath.IsAbs(mailcheck) || filepath.Clean(mailcheck) != mailcheck || mailcheck == "/") {
		return errors.New("invalid instance mailcheck path")
	}
	root := getenv("ASROBOT_RUNTIME_ROOT")
	instanceID := strings.TrimSuffix(strings.TrimPrefix(unit, "asrobot-instance-"), ".service")
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || filepath.Base(root) != instanceID {
		return errors.New("invalid isolated runtime root")
	}
	for _, value := range values[:5] {
		rel, err := filepath.Rel(root, value)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return errors.New("isolated runtime path is outside its instance root")
		}
	}
	for i := range paths {
		*paths[i] = values[i]
	}
	serviceUnit = unit
	if mailcheck != "" {
		mailcheckPath = mailcheck
	}
	isolatedRuntime = true
	return nil
}
