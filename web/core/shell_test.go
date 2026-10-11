package core_test

import (
	"strings"
	"testing"

	"poople/web/core"
)

func TestShellMastheadFollowsViewerAndCurrentPage(t *testing.T) {
	for _, tc := range []struct {
		name, title string
		viewer      *core.Viewer
		want, avoid []string
	}{
		{"anonymous", "Leaderboard", nil,
			[]string{`href="/login" aria-label="Log in" aria-current="false"`, `href="/leaderboard" aria-label="Leaderboard" aria-current="page"`, `href="/unlimited" aria-label="Unlimited" aria-current="false"`, `href="/stats" aria-label="Statistics" aria-current="false"`},
			[]string{`href="/account"`, `href="/admin/users"`}},
		{"sign up highlights log in", "Sign up", nil,
			[]string{`href="/login" aria-label="Log in" aria-current="page"`}, nil},
		{"player with avatar", "Account", &core.Viewer{Username: "mira<b>", AvatarURL: "/avatars/abc.png"},
			[]string{`href="/account" aria-label="Your account, mira&lt;b&gt;" aria-current="page"`, `<img class="avatar" src="/avatars/abc.png" alt="">`, `<span class="nav-label" aria-hidden="true">mira&lt;b&gt;</span>`},
			[]string{`href="/login"`, `href="/admin/users"`, "mira<b>"}},
		{"admin without avatar", "Users", &core.Viewer{Username: "jay", IsAdmin: true},
			[]string{`href="/admin/users" aria-label="Admin" aria-current="page"`, `href="/account" aria-label="Your account, jay" aria-current="false"><svg class="icon" aria-hidden="true"><use href="#i-user">`},
			[]string{`href="/login"`, `<img class="avatar"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			html := render(t, core.Shell(tc.title, tc.viewer))
			for _, want := range append(tc.want, `<a class="skip-link" href="#main">`, `<header class="masthead">`, `<symbol id="mascot"`, `<symbol id="i-backspace"`) {
				if !strings.Contains(html, want) {
					t.Fatalf("shell missing %q", want)
				}
			}
			for _, avoid := range tc.avoid {
				if strings.Contains(html, avoid) {
					t.Fatalf("shell contains %q", avoid)
				}
			}
		})
	}
}
