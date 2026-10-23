// Package redact keeps credentials out of what the bot logs and sends.
package redact

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// URL returns rawURL without its credentials, query, or fragment, keeping
// what identifies the endpoint.
func URL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "[invalid URL]"
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	return u.String()
}

// Error renders err with the URL of any request error in it reduced by URL.
// net/http names the request URL in its errors, and replaces only a
// password there.
func Error(err error) string {
	if err == nil {
		return ""
	}
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return err.Error()
	}
	safe := fmt.Sprintf("%s %q: %v", urlErr.Op, URL(urlErr.URL), urlErr.Err)
	return strings.Replace(err.Error(), urlErr.Error(), safe, 1)
}
