package main

import (
	"github.com/google/uuid"
	"testing"
)

func TestCustomerOperationMilestoneCommand(t *testing.T) {
	id := uuid.NewString()
	for _, tc := range []struct {
		args  []string
		valid bool
	}{
		{[]string{id, "--app", "orders"}, true},
		{[]string{id, "--self"}, true},
		{[]string{"--app", "orders", "--scope", "default", "--subject-type", "order", "--subject-id", "ord/42&é"}, true},
		{[]string{"--self", "--app", id, "--scope", "default", "--subject-type", "order", "--subject-id", "ord/42"}, true},
		{[]string{id}, false},
		{[]string{id, "--self", "--app", "orders"}, false},
		{[]string{"--app", "orders", "--subject-type", "order", "--subject-id", "42"}, false},
		{[]string{"--self", "--app", id, "--tenant", uuid.NewString(), "--scope", "default", "--subject-type", "order", "--subject-id", "42"}, false},
		{[]string{id, "--app", "orders", "--subject-type", "order", "--subject-id", "42"}, false},
		{[]string{id, "--app", "orders", "--limit", "101"}, false},
	} {
		_, err := parseCustomerMilestoneCommand(tc.args)
		if (err == nil) != tc.valid {
			t.Fatalf("%v: %v", tc.args, err)
		}
	}
}
