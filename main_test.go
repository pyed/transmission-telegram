package main

import "testing"

func TestSafeEndpointRedactsSecrets(t *testing.T) {
	got := safeEndpoint("https://user:password@example.com/transmission/rpc?token=secret#private")
	want := "https://example.com/transmission/rpc"
	if got != want {
		t.Fatalf("safeEndpoint() = %q, want %q", got, want)
	}
}

func TestSafeEndpointHandlesInvalidURL(t *testing.T) {
	if got := safeEndpoint("://invalid"); got != "[invalid URL]" {
		t.Fatalf("safeEndpoint() = %q, want invalid marker", got)
	}
}
