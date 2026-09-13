package controlplane

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/auth"
)

func TestSessionCookieIsEncryptedTenantBoundAndExpires(t *testing.T) {
	manager, err := NewSessionManager(make([]byte, 32), false, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 10, 1, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	session, err := manager.NewSession(TenantContext{ID: "tenant-a", Key: "acme"}, auth.Identity{
		TenantID: "tenant-a", ActorType: "user", ActorID: "actor", Role: auth.RoleAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	if err := manager.Set(recorder, session); err != nil {
		t.Fatal(err)
	}
	if cookie := recorder.Result().Cookies()[0]; cookie.MaxAge != int((15*time.Minute).Seconds()) || !cookie.Expires.Equal(session.ExpiresAt) {
		t.Fatalf("cookie max-age=%d expires=%s", cookie.MaxAge, cookie.Expires)
	}
	request := httptest.NewRequest("GET", "https://status.example/ui", nil)
	request.AddCookie(recorder.Result().Cookies()[0])
	got, err := manager.Read(request)
	if err != nil || got.Identity.ActorID != "actor" || got.CSRF == "" {
		t.Fatalf("session=%#v err=%v", got, err)
	}
	manager.now = func() time.Time { return now.Add(16 * time.Minute) }
	if _, err := manager.Read(request); err == nil {
		t.Fatal("expired session accepted")
	}
}
