//go:build linux

package builtin

import "testing"

func TestForbiddenProcBlocksHostEnvironmentViaSelfSymlink(t *testing.T) {
	if !confineRead(realRoots([]string{"/proc"}), "/proc/self/environ") {
		t.Fatal("host process environment remained readable through /proc/self")
	}
}
