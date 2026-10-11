//go:build !dev

// Adapted from Northstar.
/*
Copyright (c) 2025 Nicholas Zanghi

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/
package static

import (
	"io/fs"
	"net/http"

	"github.com/benbjohnson/hashfs"
)

var staticSystems = map[string]*hashfs.FS{}

func Handler(_ string, embeddedFS fs.FS, feature string) http.Handler {
	staticSystem := hashfs.NewFS(embeddedFS)
	staticSystems[feature] = staticSystem
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.StripPrefix("/"+feature, hashfs.FileServer(staticSystem)).ServeHTTP(w, r)
	})
}

func Path(feature, path string) string {
	staticSystem := staticSystems[feature]
	if staticSystem == nil {
		return "/" + feature + "/static/" + path
	}
	return "/" + feature + "/" + staticSystem.HashName("static/"+path)
}
