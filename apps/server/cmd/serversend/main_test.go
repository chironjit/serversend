package main

import "testing"

func TestCommandArguments(t *testing.T) {
	if err := run(t.Context(), []string{"-help"}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"unexpected"}, {"-unknown"}} {
		if err := run(t.Context(), args); err == nil {
			t.Fatalf("accepted invalid command arguments: %v", args)
		}
	}
}
