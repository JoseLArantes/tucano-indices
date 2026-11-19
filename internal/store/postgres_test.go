package store

import (
	"regexp"
	"testing"
)

func TestSameDecimal(t *testing.T) {
	for _, test := range []struct {
		left, right string
		want        bool
	}{
		{left: "1.2300", right: "1.23", want: true},
		{left: "-0.50", right: "-0.5", want: true},
		{left: "0", right: "0.0001", want: false},
		{left: "1", right: "2", want: false},
		{left: "invalid", right: "invalid", want: false},
	} {
		if got := sameDecimal(test.left, test.right); got != test.want {
			t.Errorf("sameDecimal(%q, %q) = %v, want %v", test.left, test.right, got, test.want)
		}
	}
}

func TestNullableDate(t *testing.T) {
	if got := nullableDate("  \t"); got != nil {
		t.Fatalf("nullableDate(blank) = %q, want nil", *got)
	}
	got := nullableDate("2026-01-31")
	if got == nil || *got != "2026-01-31" {
		t.Fatalf("nullableDate(date) = %v", got)
	}
}

func TestNewUUID(t *testing.T) {
	id, err := newUUID()
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(id) {
		t.Fatalf("newUUID() = %q, want RFC 4122 version 4 UUID", id)
	}
	second, err := newUUID()
	if err != nil {
		t.Fatal(err)
	}
	if id == second {
		t.Fatalf("consecutive UUIDs are equal: %q", id)
	}
}
