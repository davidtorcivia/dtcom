package main

import "testing"

func TestVersionConstant(t *testing.T) {
	if Version == "" {
		t.Fatal("Version must not be empty")
	}
}
