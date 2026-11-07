package service

import (
	"slices"

	"github.com/Varsh1l01/VENDORA/pkg/apperr"
)

// Status is an order lifecycle state.
type Status string

// Order states.
const (
	Pending         Status = "PENDING"
	AwaitingPayment Status = "AWAITING_PAYMENT"
	Confirmed       Status = "CONFIRMED"
	Shipped         Status = "SHIPPED"
	Delivered       Status = "DELIVERED"
	Cancelled       Status = "CANCELLED"
	Rejected        Status = "REJECTED"
)

// transitions is the order state machine. Anything not listed is illegal.
//
//	PENDING ──reserve ok──▶ AWAITING_PAYMENT ──paid──▶ CONFIRMED ──▶ SHIPPED ──▶ DELIVERED
//	   │ └─insufficient stock─▶ REJECTED        │
//	   └──────────────┬─────────────────────────┘
//	                  ▼ (user cancel / payment failed / timeout)
//	              CANCELLED
var transitions = map[Status][]Status{
	Pending:         {AwaitingPayment, Rejected, Cancelled},
	AwaitingPayment: {Confirmed, Cancelled},
	Confirmed:       {Shipped},
	Shipped:         {Delivered},
}

// CanTransition reports whether from -> to is allowed.
func CanTransition(from, to Status) bool {
	return slices.Contains(transitions[from], to)
}

// SourcesOf returns every state that may transition to to.
func SourcesOf(to Status) []Status {
	var out []Status
	for from, tos := range transitions {
		if slices.Contains(tos, to) {
			out = append(out, from)
		}
	}
	slices.Sort(out)
	return out
}

// IsTerminal reports whether no transition leaves s.
func IsTerminal(s Status) bool { return len(transitions[s]) == 0 }

// ParseStatus validates a status string.
func ParseStatus(s string) (Status, error) {
	switch st := Status(s); st {
	case Pending, AwaitingPayment, Confirmed, Shipped, Delivered, Cancelled, Rejected:
		return st, nil
	}
	return "", apperr.Invalid("unknown order status %q", s)
}
