package admin

import (
	"bytes"
	"io/fs"
	"strings"
	"testing"
)

// TestTemplatesRender is a smoke test guaranteeing every embedded admin
// template parses and renders against representative data. We invoke the
// real parsePages helper rather than New (which needs a live pool).
func TestTemplatesRender(t *testing.T) {
	sub, err := fs.Sub(templatesFS, "templates")
	if err != nil {
		t.Fatalf("sub fs: %v", err)
	}
	pages, err := parsePages(sub)
	if err != nil {
		t.Fatalf("parse pages: %v", err)
	}

	cases := []struct {
		name string
		data any
		want string
	}{
		{"login.html", map[string]any{"Title": "Sign in", "Error": ""}, "Sign in"},
		{"dashboard.html", map[string]any{
			"Title": "Dashboard", "NavActive": "dashboard",
			"FeedbackCount": 1, "TokenCount": 0, "BroadcastCount": 0,
			"RecentFeedback": nil,
		}, "Latest feedback"},
		{"feedback.html", map[string]any{
			"Title": "Feedback", "NavActive": "feedback", "Entries": nil,
		}, "No feedback yet"},
		{"broadcasts.html", map[string]any{
			"Title": "Broadcasts", "NavActive": "broadcasts", "Items": nil,
		}, "No broadcasts sent yet"},
		{"broadcast_new.html", map[string]any{
			"Title": "New broadcast", "NavActive": "broadcasts", "Count": 0,
		}, "Send"},
		{"broadcast_result.html", map[string]any{
			"Recipients": 3, "Delivered": 2, "Failed": 1, "Pruned": 0,
		}, "Delivered"},
		{"_recipient_count.html", map[string]any{"Count": 4}, "4 devices"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpl, ok := pages[tc.name]
			if !ok {
				t.Fatalf("missing page: %s", tc.name)
			}
			var buf bytes.Buffer
			var err error
			if tmpl.Lookup("layout") != nil {
				err = tmpl.ExecuteTemplate(&buf, "layout", tc.data)
			} else {
				err = tmpl.ExecuteTemplate(&buf, tc.name, tc.data)
			}
			if err != nil {
				t.Fatalf("execute %s: %v", tc.name, err)
			}
			if !strings.Contains(buf.String(), tc.want) {
				t.Fatalf("missing %q in output:\n%s", tc.want, buf.String())
			}
		})
	}
}
