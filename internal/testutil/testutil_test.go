package testutil

import "testing"

func TestTestdataRoot(t *testing.T) {
	root := TestdataRoot(t)
	if root == "" {
		t.Fatal("expected non-empty testdata root")
	}
}
