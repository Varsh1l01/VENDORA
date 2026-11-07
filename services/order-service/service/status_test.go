package service

import "testing"

func TestStateMachine(t *testing.T) {
	all := []Status{Pending, AwaitingPayment, Confirmed, Shipped, Delivered, Cancelled, Rejected}
	allowed := map[[2]Status]bool{
		{Pending, AwaitingPayment}:   true,
		{Pending, Rejected}:          true,
		{Pending, Cancelled}:         true,
		{AwaitingPayment, Confirmed}: true,
		{AwaitingPayment, Cancelled}: true,
		{Confirmed, Shipped}:         true,
		{Shipped, Delivered}:         true,
	}
	for _, from := range all {
		for _, to := range all {
			want := allowed[[2]Status{from, to}]
			if got := CanTransition(from, to); got != want {
				t.Errorf("%s -> %s: got %v, want %v", from, to, got, want)
			}
		}
	}
	for _, s := range []Status{Delivered, Cancelled, Rejected} {
		if !IsTerminal(s) {
			t.Errorf("%s must be terminal", s)
		}
	}
	// A paid order can never be cancelled through the normal flow (that
	// would require a refund workflow), and a cancelled order never revives.
	if CanTransition(Confirmed, Cancelled) || CanTransition(Cancelled, Confirmed) {
		t.Error("illegal transition allowed")
	}
}

func TestSourcesOf(t *testing.T) {
	got := SourcesOf(Cancelled)
	if len(got) != 2 || got[0] != AwaitingPayment || got[1] != Pending {
		t.Fatalf("SourcesOf(CANCELLED) = %v", got)
	}
}

func TestParseStatus(t *testing.T) {
	if _, err := ParseStatus("SHIPPED"); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseStatus("LOST_IN_SPACE"); err == nil {
		t.Fatal("unknown status accepted")
	}
}
