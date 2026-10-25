package tool

import "context"

// ForegroundAuthorization is attached by the controller, never tool arguments.
type ForegroundAuthorization struct {
	MessageID         string
	Text              string
	PreviousAssistant string
}
type foregroundAuthorizationKey struct{}

func WithForegroundAuthorization(ctx context.Context, a ForegroundAuthorization) context.Context {
	return context.WithValue(ctx, foregroundAuthorizationKey{}, a)
}
func ForegroundAuthorizationFrom(ctx context.Context) (ForegroundAuthorization, bool) {
	a, ok := ctx.Value(foregroundAuthorizationKey{}).(ForegroundAuthorization)
	return a, ok && a.MessageID != "" && a.Text != ""
}
