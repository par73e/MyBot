package bot

import "reasonix/internal/event"

func (s *renderSink) emitAsk(ask event.Ask) {
	if s.onAsk != nil {
		s.onAsk(ask)
	}
	text := renderAskText(ask)
	msg := s.textMessage(text)
	if s.adapter.Platform() == PlatformFeishu {
		msg.Card = askCard(ask, text, s.chatType, s.userID)
	}
	_ = s.send(msg)
}

func (s *renderSink) emitApproval(approval event.Approval) {
	if s.onApproval != nil {
		s.onApproval(approval)
	}
	msg := OutboundMessage{
		ConnectionID: s.connID,
		Domain:       s.domain,
		ChatID:       s.chatID,
		ChatType:     s.chatType,
		Text:         renderApprovalText(approval),
		ReplyToMsgID: s.replyTo,
	}
	switch s.adapter.Platform() {
	case PlatformQQ:
		switch {
		case isRecoveryApproval(approval):
			msg.Keyboard = recoveryKeyboard(approval)
		case isWriteAccessApproval(approval):
			msg.Keyboard = writeAccessKeyboard(approval.ID)
		default:
			msg.Keyboard = approvalKeyboard(approval.ID)
		}
	case PlatformFeishu:
		switch {
		case isRecoveryApproval(approval):
			msg.Card = recoveryCard(approval, s.chatType, s.userID)
		case isWriteAccessApproval(approval):
			msg.Card = writeAccessCard(approval, s.chatType, s.userID)
		default:
			msg.Card = approvalCard(approval, s.chatType, s.userID)
		}
	}
	_ = s.send(msg)
}
