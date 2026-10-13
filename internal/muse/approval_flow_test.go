package muse

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMUSEApprovalDecisions(t *testing.T) {
	for _, decision := range []Decision{DecisionAllow, DecisionDeny} {
		t.Run(string(decision), func(t *testing.T) {
			store := NewApprovalStore()
			item := store.Request(Request{
				SessionID: "flow-test",
				Action:    ActionWriteFile,
				Target:    "test.txt",
			})

			done := make(chan error, 1)
			go func() {
				time.Sleep(25 * time.Millisecond)
				done <- store.Decide(item.ID, decision)
			}()

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			got, err := store.Wait(ctx, item, PolicyFor(ActionWriteFile))
			if err != nil {
				t.Fatalf("wait: %v", err)
			}
			if got != decision {
				t.Fatalf("decision = %q, want %q", got, decision)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if len(store.Pending()) != 0 {
				t.Fatal("resolved approval remains pending")
			}
		})
	}
}

func TestMUSEApprovalCancellationCleanup(t *testing.T) {
	store := NewApprovalStore()
	item := store.Request(Request{Action: ActionWriteFile})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := store.Wait(ctx, item, PolicyFor(ActionWriteFile))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if len(store.Pending()) != 0 {
		t.Fatal("cancelled approval remains pending")
	}
}

func TestMUSEApprovalTimeoutDenyCleanup(t *testing.T) {
	store := NewApprovalStore()
	item := store.Request(Request{Action: ActionWriteFile})

	policy := Policy{
		RequireApproval: true,
		TimeoutSeconds:  1,
		OnTimeout:       TimeoutDeny,
	}

	got, err := store.Wait(context.Background(), item, policy)
	if got != DecisionDeny || !errors.Is(err, ErrApprovalDenied) {
		t.Fatalf("timeout result = %q, error = %v", got, err)
	}
	if len(store.Pending()) != 0 {
		t.Fatal("timed-out approval remains pending")
	}
}
