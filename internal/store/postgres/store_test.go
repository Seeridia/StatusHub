package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type stubDatabase struct {
	tag       pgconn.CommandTag
	err       error
	query     string
	arguments []any
}

func (s *stubDatabase) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	return nil, errors.New("unexpected transaction")
}

func (s *stubDatabase) Exec(_ context.Context, query string, arguments ...any) (pgconn.CommandTag, error) {
	s.query = query
	s.arguments = arguments
	return s.tag, s.err
}

func (*stubDatabase) Ping(context.Context) error { return nil }
func (*stubDatabase) Close()                     {}

func TestCompletePollRejectsStaleLease(t *testing.T) {
	database := &stubDatabase{tag: pgconn.NewCommandTag("UPDATE 0")}
	store := &Store{db: database}

	err := store.CompletePoll(context.Background(), CompletePollParams{
		SourceID:   "00000000-0000-0000-0000-000000000001",
		LeaseToken: "00000000-0000-0000-0000-000000000002",
		NextPollAt: time.Now().Add(time.Minute),
	})
	if !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("CompletePoll error = %v, want ErrLeaseLost", err)
	}
	if !strings.Contains(database.query, "lease_token = $2") {
		t.Fatalf("CompletePoll query is not fenced by lease token: %s", database.query)
	}
}

func TestFailPollReleasesOnlyMatchingLease(t *testing.T) {
	database := &stubDatabase{tag: pgconn.NewCommandTag("UPDATE 1")}
	store := &Store{db: database}

	err := store.FailPoll(context.Background(), FailPollParams{
		SourceID:   "00000000-0000-0000-0000-000000000001",
		LeaseToken: "00000000-0000-0000-0000-000000000002",
		NextPollAt: time.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("FailPoll returned error: %v", err)
	}
	if !strings.Contains(database.query, "lease_token = $2") {
		t.Fatalf("FailPoll query is not fenced by lease token: %s", database.query)
	}
}

func TestMarkOutboxPublishedRejectsStaleLease(t *testing.T) {
	database := &stubDatabase{tag: pgconn.NewCommandTag("UPDATE 0")}
	store := &Store{db: database}

	err := store.MarkOutboxPublished(
		context.Background(),
		"00000000-0000-0000-0000-000000000003",
		"00000000-0000-0000-0000-000000000004",
		time.Time{},
	)
	if !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("MarkOutboxPublished error = %v, want ErrLeaseLost", err)
	}
	if !strings.Contains(database.query, "lease_token = $2") {
		t.Fatalf("publish query is not fenced by lease token: %s", database.query)
	}
}

func TestAcquireValidationDoesNotStartTransaction(t *testing.T) {
	store := &Store{db: &stubDatabase{}}

	_, err := store.AcquireDueSources(context.Background(), "", 1, time.Minute)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("AcquireDueSources error = %v, want ErrInvalidArgument", err)
	}
	_, err = store.ClaimOutbox(context.Background(), "worker", 0, time.Minute)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ClaimOutbox error = %v, want ErrInvalidArgument", err)
	}
}
