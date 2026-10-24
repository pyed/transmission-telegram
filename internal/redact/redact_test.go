package redact

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestURLDropsCredentialsQueryAndFragment(t *testing.T) {
	got := URL("https://user:password@example.com/transmission/rpc?token=secret#private")
	if want := "https://example.com/transmission/rpc"; got != want {
		t.Fatalf("URL() = %q, want %q", got, want)
	}
}

func TestURLHandlesInvalidURL(t *testing.T) {
	if got := URL("://invalid"); got != "[invalid URL]" {
		t.Fatalf("URL() = %q, want invalid marker", got)
	}
}

// Request errors name their URL, and net/http masks only a password in it;
// the user name and query tokens stay. Context wrapped around the error
// must survive.
func TestErrorRedactsTheURLOfRequestErrors(t *testing.T) {
	request := &url.Error{
		Op:  "Post",
		URL: "http://admin:***@transmission:9091/transmission/rpc?token=secret",
		Err: errors.New("dial tcp 10.0.0.2:9091: connect: connection refused"),
	}
	got := Error(fmt.Errorf("get session: %w", request))
	want := `get session: Post "http://transmission:9091/transmission/rpc": dial tcp 10.0.0.2:9091: connect: connection refused`
	if got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	for _, secret := range []string{"admin", "token", "secret"} {
		if strings.Contains(got, secret) {
			t.Errorf("Error() kept %q: %q", secret, got)
		}
	}
}

func TestErrorKeepsOtherErrors(t *testing.T) {
	if got := Error(errors.New("transmission RPC error (HTTP 401)")); got != "transmission RPC error (HTTP 401)" {
		t.Fatalf("Error() = %q", got)
	}
	if got := Error(nil); got != "" {
		t.Fatalf("Error(nil) = %q", got)
	}
}
