package bot

import (
	"context"
	"errors"
	"strings"

	"reasonix/internal/event"
)

// emitFinalOnly filters presentation events, never the controller's execution
// or durable transcript. Message commits one model round, not the whole task:
// retain only the latest accepted answer, invalidate it when work continues,
// and deliver at TurnDone. Never fall back to raw deltas or reasoning.
func (s *renderSink) emitFinalOnly(e event.Event) bool {
	switch e.Kind {
	case event.TurnStarted:
		s.finalText, s.finalDone = "", false
		return false // Reset the ordinary rendering state as well.
	case event.ApprovalRequest, event.AskRequest:
		return false // Required interactions must still unblock the turn.
	case event.Message:
		s.finalText = strings.TrimSpace(e.Text)
	case event.ToolDispatch:
		if !e.Tool.Refreshed {
			s.finalText = ""
		}
	case event.StreamAttempt:
		switch e.StreamAttempt.Action {
		case event.StreamAttemptBegin, event.StreamAttemptDiscard:
			s.finalText = ""
		case event.StreamAttemptCommit:
			if r := e.StreamAttempt.Response; r != nil && (r.ToolCalls > 0 || r.TextBytes == 0 || !r.StreamComplete) {
				s.finalText = ""
			}
		}
	case event.TurnDone:
		// The controller and gateway can both announce completion.
		if s.finalDone {
			return true
		}
		s.finalDone = true
		if e.Cancelled || errors.Is(e.Err, context.Canceled) || s.ctx.Err() != nil {
			s.finalText = ""
			return true
		}
		text := s.finalText
		if e.Err != nil {
			s.logger.Warn("bot final-only turn failed", "err", e.Err)
			text = "Sir，本次处理失败，未能生成完整答复，请稍后重试。"
		} else if text == "" {
			text = "Sir，本次未能生成完整答复，请重试。"
		}
		s.finalText = ""
		s.buf.Reset()
		s.buf.WriteString(text)
		s.flush() // Preserve platform length limits for long final answers.
	}
	// Everything else (deltas, reasoning, tool status/errors, maintenance,
	// retry/warning notices) stays on internal event/log surfaces.
	return true
}
