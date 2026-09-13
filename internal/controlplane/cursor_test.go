package controlplane

import (
	"strings"
	"testing"
	"time"

	store "github.com/vendor-status-monitoring/vendor-status-monitoring/internal/store/postgres"
)

func TestCursorCodecRejectsTamperingAndCrossTenantUse(t *testing.T) {
	codec, err := NewCursorCodec(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	want := store.TimeCursor{Time: time.Unix(123, 456).UTC(), ID: "10000000-0000-0000-0000-000000000001"}
	encoded, err := codec.EncodeTime("incidents", "tenant-a", want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := codec.DecodeTime(encoded, "incidents", "tenant-a")
	if err != nil || !got.Time.Equal(want.Time) || got.ID != want.ID {
		t.Fatalf("got=%#v err=%v", got, err)
	}
	if _, err := codec.DecodeTime(encoded, "incidents", "tenant-b"); err == nil {
		t.Fatal("cross-tenant cursor was accepted")
	}
	tampered := encoded[:len(encoded)-1] + strings.ToUpper(encoded[len(encoded)-1:])
	if tampered == encoded {
		tampered = encoded[:len(encoded)-1] + "A"
	}
	if _, err := codec.DecodeTime(tampered, "incidents", "tenant-a"); err == nil {
		t.Fatal("tampered cursor was accepted")
	}
}
