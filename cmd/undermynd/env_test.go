package main

import (
	"os"
	"testing"
)

func TestEnvAliases(t *testing.T) {
	t.Setenv("HYVEMYND_TEST_A", "new")
	t.Setenv("HYVEMYND_TEST_B", "new")
	t.Setenv("UNDERMYND_TEST_B", "old")
	os.Unsetenv("UNDERMYND_TEST_A")
	defer os.Unsetenv("UNDERMYND_TEST_A")
	envAliases()
	if os.Getenv("UNDERMYND_TEST_A") != "new" || os.Getenv("UNDERMYND_TEST_B") != "old" {
		t.Fatal(os.Getenv("UNDERMYND_TEST_A"), os.Getenv("UNDERMYND_TEST_B"))
	}
}
