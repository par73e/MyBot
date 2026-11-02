package assistant

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServerHealthUnavailableAndAction(t *testing.T) {
	result, err := readServerHealth(context.Background(), filepath.Join(t.TempDir(), "missing"), time.Now())
	if err != nil || !strings.Contains(result, `"unknown"`) {
		t.Fatalf("unexpected %s %v", result, err)
	}
	if _, err = (ServerHealthTool{}).Execute(context.Background(), []byte(`{"action":"restart"}`)); err == nil {
		t.Fatal("mutation accepted")
	}
	if _, err = (ServerHealthTool{}).Execute(context.Background(), []byte(`{"action":"status","url":"x"}`)); err == nil {
		t.Fatal("arbitrary argument accepted")
	}
}
func TestServerHealthFreshness(t *testing.T) {
	for _, age := range []time.Duration{0, 2 * time.Minute} {
		t.Run(age.String(), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "s")
			l, err := net.Listen("unix", path)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			now := time.Now().UTC()
			go func() {
				c, e := l.Accept()
				if e != nil {
					return
				}
				defer c.Close()
				buf := make([]byte, 32)
				_, _ = c.Read(buf)
				fmt.Fprintf(c, `{"schema_version":1,"collected_at":%q,"items":{"disk":{"status":"ok"}}}`, now.Add(-age).Format(time.RFC3339Nano))
			}()
			got, err := readServerHealth(context.Background(), path, now)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(got, `"unknown"`) != (age > time.Minute) {
				t.Fatal(got)
			}
		})
	}
}

func TestServerHealthDescriptionExplainsUnmonitoredAndNoThresholds(t *testing.T) {
	text := (ServerHealthTool{}).Description()
	for _, phrase := range []string{"not_monitored", "deliberately not checked", "no good/bad", "do not claim overall healthy"} {
		if !strings.Contains(text, phrase) {
			t.Fatalf("missing interpretation boundary %q", phrase)
		}
	}
}
