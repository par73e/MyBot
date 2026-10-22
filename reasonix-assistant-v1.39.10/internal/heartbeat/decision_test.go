package heartbeat

import "testing"

func TestNotificationDecision(t *testing.T) {
	for _, tc := range []struct {
		raw, want string
		bad       bool
	}{
		{`{"notify":false,"message":"must not leak","reason":"unchanged"}`, "", false},
		{`{"notify":true,"message":"important reply","reason":"action required"}`, "important reply", false},
		{`{"message":"missing decision","reason":"x"}`, "", true},
		{`{"notify":true,"message":"","reason":"x"}`, "", true},
		{`{"notify":false,"message":"","reason":""}`, "", true},
		{"plain analysis must never be pushed", "", true},
		{`{"notify":true,"message":"x","reason":"y"} {}`, "", true},
	} {
		got, err := NotificationText(tc.raw)
		if (err != nil) != tc.bad || got != tc.want {
			t.Errorf("%s: %q %v", tc.raw, got, err)
		}
	}
}
