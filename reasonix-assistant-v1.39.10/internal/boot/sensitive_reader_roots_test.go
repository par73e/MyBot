package boot

import (
	"slices"
	"testing"
)

func TestSensitiveReaderRootsProtectProcOnlyWhenEnabledOnLinux(t *testing.T) {
	base := []string{"/private/file"}
	if got := sensitiveReaderRoots(base, "linux", true); !slices.Contains(got, "/proc") || !slices.Contains(got, base[0]) {
		t.Fatalf("missing protected reader roots: %v", got)
	}
	for _, test := range []struct {
		os      string
		enabled bool
	}{{"linux", false}, {"darwin", true}} {
		if got := sensitiveReaderRoots(base, test.os, test.enabled); slices.Contains(got, "/proc") {
			t.Fatalf("unexpected /proc protection for %s: %v", test.os, got)
		}
	}
	if len(base) != 1 {
		t.Fatal("modified caller's slice")
	}
}
