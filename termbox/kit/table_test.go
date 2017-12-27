package kit

import (
	"testing"
)

func TestStringWidth(t *testing.T) {
	data := []struct {
		Name   string
		Input  string
		Expect int
	}{
		{"A,combining-ring-above", "A\u030A", 1},
		{"Angstrom", "\u212B", 1},
	}

	for _, d := range data {
		result := String(d.Input).Width()
		if d.Expect != result {
			t.Fatalf("%s: expected: %s but was: %s", d.Name, d.Expect, result)
		}
	}
}
