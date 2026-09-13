package auth

import "context"

type invitationContextKey struct{}

func WithInvitation(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, invitationContextKey{}, token)
}
func InvitationFromContext(ctx context.Context) string {
	v, _ := ctx.Value(invitationContextKey{}).(string)
	return v
}
