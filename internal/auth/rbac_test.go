package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type verifierFunc func(context.Context, string, string) (Identity, error)

func (function verifierFunc) Authenticate(ctx context.Context, tenantID, token string) (Identity, error) {
	return function(ctx, tenantID, token)
}

func TestRoleMatrixAndTenantBoundary(t *testing.T) {
	if Allowed(RoleViewer, PermissionEndpointWrite) || !Allowed(RoleOperator, PermissionDeliveryReplay) || Allowed(RoleAdmin, PermissionRegionFailover) || !Allowed(RoleOwner, PermissionRegionFailover) {
		t.Fatal("unexpected role permission matrix")
	}
	verifier := verifierFunc(func(_ context.Context, _ string, token string) (Identity, error) {
		if token == "bad" {
			return Identity{}, ErrUnauthenticated
		}
		return Identity{TenantID: "tenant-a", ActorID: "user-1", Role: RoleAdmin}, nil
	})
	next := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if identity, ok := IdentityFromContext(request.Context()); !ok || identity.ActorID != "user-1" {
			t.Fatal("identity missing from request context")
		}
		response.WriteHeader(http.StatusNoContent)
	})
	handler := Require(verifier, func(*http.Request) (string, error) { return "tenant-a", nil }, PermissionAuditExport, next)
	request := httptest.NewRequest(http.MethodGet, "/audit", nil)
	request.Header.Set("Authorization", "Bearer good")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d", response.Code)
	}

	crossTenant := Require(verifier, func(*http.Request) (string, error) { return "tenant-b", nil }, PermissionRead, next)
	response = httptest.NewRecorder()
	crossTenant.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant status=%d", response.Code)
	}
	missingTenant := Require(verifier, func(*http.Request) (string, error) { return "", errors.New("missing") }, PermissionRead, next)
	response = httptest.NewRecorder()
	missingTenant.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("missing tenant status=%d", response.Code)
	}
}
