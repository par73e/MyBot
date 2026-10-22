// Package heartbeat persists and dispatches scheduled work without a desktop.
// The calendar matcher is ported from desktop/heartbeat_schedule.go; execution
// and delivery are injected by the CLI so this package has no frontend imports.
package heartbeat
