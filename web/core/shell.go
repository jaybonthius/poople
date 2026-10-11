package core

import (
	"context"
	"fmt"
	"io"

	"poople/lib/static"

	"github.com/a-h/templ"
)

// Viewer is the signed-in player shown in the masthead. Features build it from
// the identity service so core does not import identity; nil means anonymous.
type Viewer struct {
	Username string
	// AvatarURL is the player's picture; empty shows the account icon.
	AvatarURL string
	// IsAdmin adds the admin link to the masthead.
	IsAdmin bool
}

func currentPage(current bool) string {
	if current {
		return "page"
	}
	return "false"
}

// fontFaces renders the @font-face rules in the shell so their URLs match the
// preload links in both disk-served and hashed builds.
func fontFaces() templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		css := fmt.Sprintf(`@font-face{font-family:'Fredoka';src:url(%q) format('woff2');font-weight:300 700;font-stretch:75%% 125%%;font-display:swap;}@font-face{font-family:'Figtree';src:url(%q) format('woff2');font-weight:300 900;font-display:swap;}`,
			static.Path("core", "fonts/fredoka-latin.woff2"), static.Path("core", "fonts/figtree-latin.woff2"))
		return templ.RenderCSSItems(ctx, w, templ.ComponentCSSClass{ID: "poople-font-faces", Class: templ.SafeCSS(css)})
	})
}
