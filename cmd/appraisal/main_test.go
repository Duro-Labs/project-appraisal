package main

import "testing"

func TestLoopback(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:7790":   true,
		"localhost:7790":   true,
		"[::1]:7790":       true,
		"127.0.0.2:1":      true,
		"0.0.0.0:7790":     false,
		"192.168.1.5:7790": false,
		"example.com:80":   false,
		":7790":            false,
		"7790":             false,
		"":                 false,
	}
	for addr, want := range cases {
		if got := loopback(addr); got != want {
			t.Errorf("loopback(%q) = %v, want %v", addr, got, want)
		}
	}
}
