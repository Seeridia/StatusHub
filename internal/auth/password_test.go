package auth

import (
	"strings"
	"testing"
)

func TestPasswordAndRoleBoundaries(t *testing.T) {
	h, e := HashPassword("a long passphrase 123")
	if e != nil || !VerifyPassword(h, "a long passphrase 123") || VerifyPassword(h, "wrong") {
		t.Fatal("password verification failed")
	}
	for _, p := range []string{"short", strings.Repeat("界", 129)} {
		if _, e := HashPassword(p); e == nil {
			t.Fatal("invalid length accepted")
		}
	}
	for _, actor := range []Role{RoleViewer, RoleOperator, RoleAdmin, RoleOwner} {
		for _, old := range []Role{RoleViewer, RoleOperator, RoleAdmin, RoleOwner} {
			for _, next := range []Role{RoleViewer, RoleOperator, RoleAdmin, RoleOwner} {
				want := actor == RoleOwner || (actor == RoleAdmin && (old == RoleViewer || old == RoleOperator) && (next == RoleViewer || next == RoleOperator))
				if CanManage(actor, old, next) != want {
					t.Fatal("role boundary")
				}
			}
		}
	}
}
