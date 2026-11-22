package service

import "testing"

func TestValidModelName(t *testing.T) {
	cases := map[string]bool{
		"gpt-4o-mini":          true,
		"cx/gpt-6-sol":         true,
		"ag/claude-sonnet-5-5": true,
		"org/team/model":       true,
		"cx/gpt-6-sol[1m]":     true,
		"":                     false,
		"has space":            false,
		"/leading":             false,
		"trailing/":            false,
		"double//slash":        false,
		"query?x":              false,
		"frag#x":               false,
	}
	for in, want := range cases {
		if got := validModelName(in); got != want {
			t.Errorf("validModelName(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestValidAliasRejectsSlash(t *testing.T) {
	if !validAlias("smart") {
		t.Error("validAlias(smart) = false, want true")
	}
	if validAlias("cx/gpt-6-sol") {
		t.Error("validAlias(cx/gpt-6-sol) = true, want false")
	}
}
