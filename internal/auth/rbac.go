// Package auth contains tenant-scoped authentication and authorization. A
// caller must supply the tenant from a trusted route/resource lookup; token
// claims never select the tenant by themselves.
package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

var (
	ErrUnauthenticated = errors.New("auth: unauthenticated")
	ErrForbidden       = errors.New("auth: forbidden")
)

type Role string

const (
	RoleViewer   Role = "viewer"
	RoleOperator Role = "operator"
	RoleAdmin    Role = "admin"
	RoleOwner    Role = "owner"
)

func (role Role) Valid() bool {
	switch role {
	case RoleViewer, RoleOperator, RoleAdmin, RoleOwner:
		return true
	default:
		return false
	}
}

type Permission string

const (
	PermissionRead              Permission = "resource.read"
	PermissionDeliveryReplay    Permission = "delivery.replay"
	PermissionSubscriptionWrite Permission = "subscription.write"
	PermissionEndpointWrite     Permission = "endpoint.write"
	PermissionConnectorWrite    Permission = "connector.write"
	PermissionPrivateAgentWrite Permission = "private_agent.write"
	PermissionMemberWrite       Permission = "member.write"
	PermissionIdentityWrite     Permission = "identity.write"
	PermissionAuditExport       Permission = "audit.export"
	PermissionRegionFailover    Permission = "region.failover"
	PermissionAdapterRollout    Permission = "adapter.rollout"
)

var rolePermissions = map[Role]map[Permission]struct{}{
	RoleViewer: {
		PermissionRead: {},
	},
	RoleOperator: {
		PermissionRead: {}, PermissionDeliveryReplay: {}, PermissionSubscriptionWrite: {},
		PermissionEndpointWrite: {},
	},
	RoleAdmin: {
		PermissionRead: {}, PermissionDeliveryReplay: {}, PermissionSubscriptionWrite: {},
		PermissionEndpointWrite: {}, PermissionConnectorWrite: {}, PermissionPrivateAgentWrite: {},
		PermissionMemberWrite: {}, PermissionAuditExport: {}, PermissionAdapterRollout: {},
	},
	RoleOwner: {
		PermissionRead: {}, PermissionDeliveryReplay: {}, PermissionSubscriptionWrite: {},
		PermissionEndpointWrite: {}, PermissionConnectorWrite: {}, PermissionPrivateAgentWrite: {},
		PermissionMemberWrite: {}, PermissionIdentityWrite: {}, PermissionAuditExport: {},
		PermissionRegionFailover: {}, PermissionAdapterRollout: {},
	},
}

func Allowed(role Role, permission Permission) bool {
	_, ok := rolePermissions[role][permission]
	return ok
}

type Identity struct {
	CredentialVersion string `json:"-"`
	TenantID          string `json:"tenant_id"`
	ActorType         string `json:"actor_type"`
	ActorID           string `json:"actor_id"`
	Issuer            string `json:"issuer,omitempty"`
	Subject           string `json:"subject,omitempty"`
	Email             string `json:"email,omitempty"`
	DisplayName       string `json:"display_name,omitempty"`
	Role              Role   `json:"role"`
}

type Verifier interface {
	Authenticate(context.Context, string, string) (Identity, error)
}

type ServiceAccountRepository interface {
	AuthenticateServiceAccount(context.Context, string, string) (Identity, error)
}

type CompositeVerifier struct {
	oidc            *OIDCAuthenticator
	serviceAccounts ServiceAccountRepository
}

func NewCompositeVerifier(oidc *OIDCAuthenticator, serviceAccounts ServiceAccountRepository) (*CompositeVerifier, error) {
	if oidc == nil || serviceAccounts == nil {
		return nil, errors.New("auth: OIDC and service account authenticators are required")
	}
	return &CompositeVerifier{oidc: oidc, serviceAccounts: serviceAccounts}, nil
}

func (verifier *CompositeVerifier) Authenticate(ctx context.Context, tenantID, token string) (Identity, error) {
	if strings.HasPrefix(token, "sa.") {
		identity, err := verifier.serviceAccounts.AuthenticateServiceAccount(ctx, tenantID, token)
		if err != nil {
			return Identity{}, ErrUnauthenticated
		}
		return identity, nil
	}
	return verifier.oidc.Authenticate(ctx, tenantID, token)
}

func (verifier *CompositeVerifier) AuthenticateOIDC(ctx context.Context, tenantID, token, nonce string) (Identity, error) {
	if verifier == nil || verifier.oidc == nil || strings.HasPrefix(token, "sa.") {
		return Identity{}, ErrUnauthenticated
	}
	return verifier.oidc.AuthenticateWithNonce(ctx, tenantID, token, nonce)
}

type TenantResolver func(*http.Request) (string, error)

type contextKey struct{}

func IdentityFromContext(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(contextKey{}).(Identity)
	return identity, ok
}

func Require(verifier Verifier, resolveTenant TenantResolver, permission Permission, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if verifier == nil || resolveTenant == nil || next == nil {
			http.Error(response, "authorization unavailable", http.StatusServiceUnavailable)
			return
		}
		tenantID, err := resolveTenant(request)
		if err != nil || strings.TrimSpace(tenantID) == "" {
			http.Error(response, "forbidden", http.StatusForbidden)
			return
		}
		token, ok := bearerToken(request.Header.Get("Authorization"))
		if !ok {
			response.Header().Set("WWW-Authenticate", `Bearer realm="statusmon"`)
			http.Error(response, "unauthorized", http.StatusUnauthorized)
			return
		}
		identity, err := verifier.Authenticate(request.Context(), tenantID, token)
		if err != nil {
			response.Header().Set("WWW-Authenticate", `Bearer realm="statusmon", error="invalid_token"`)
			http.Error(response, "unauthorized", http.StatusUnauthorized)
			return
		}
		if identity.TenantID != tenantID || !Allowed(identity.Role, permission) {
			http.Error(response, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(response, request.WithContext(context.WithValue(request.Context(), contextKey{}, identity)))
	})
}

func bearerToken(value string) (string, bool) {
	parts := strings.Fields(value)
	returnValue := ""
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		returnValue = parts[1]
	}
	return returnValue, returnValue != ""
}
