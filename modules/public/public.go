// Copyright 2016 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package public

import (
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"time"

	"code.gitea.io/gitea/modules/setting"
)

// Options represents the available options to configure the macaron handler.
type Options struct {
	Directory   string
	IndexFile   string
	SkipLogging bool
	// if set to true, will enable caching. Expires header will also be set to
	// expire after the defined time.
	ExpiresAfter time.Duration
	FileSystem   http.FileSystem
	Prefix       string
}

// KnownPublicEntries list all direct children in the `public` directory
var KnownPublicEntries = []string{
	"css",
	"img",
	"js",
	"serviceworker.js",
	"vendor",
}

// Custom implements the macaron static handler for serving custom assets.
func Custom(opts *Options) func(next http.Handler) http.Handler {
	return opts.staticHandler(path.Join(setting.CustomPath, "public"))
}

// staticFileSystem implements http.FileSystem interface.
type staticFileSystem struct {
	dir *http.Dir
}

func newStaticFileSystem(directory string) staticFileSystem {
	if !filepath.IsAbs(directory) {
		directory = filepath.Join(setting.AppWorkPath, directory)
	}
	dir := http.Dir(directory)
	return staticFileSystem{&dir}
}

func (fs staticFileSystem) Open(name string) (http.File, error) {
	return fs.dir.Open(name)
}

// StaticHandler sets up a new middleware for serving static files in the
func StaticHandler(dir string, opts *Options) func(next http.Handler) http.Handler {
	return opts.staticHandler(dir)
}

func (opts *Options) staticHandler(dir string) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		// Defaults
		if len(opts.IndexFile) == 0 {
			opts.IndexFile = "index.html"
		}
		// Normalize the prefix if provided
		if opts.Prefix != "" {
			// Ensure we have a leading '/'
			if opts.Prefix[0] != '/' {
				opts.Prefix = "/" + opts.Prefix
			}
			// Remove any trailing '/'
			opts.Prefix = strings.TrimRight(opts.Prefix, "/")
		}
		if opts.FileSystem == nil {
			opts.FileSystem = newStaticFileSystem(dir)
		}

		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if !opts.handle(w, req, opts) {
				next.ServeHTTP(w, req)
			}
		})
	}
}

func (opts *Options) handle(w http.ResponseWriter, req *http.Request, opt *Options) bool {
	if req.Method != "GET" && req.Method != "HEAD" {
		return false
	}

	file := req.URL.Path
	// if we have a prefix, filter requests by stripping the prefix
	if opt.Prefix != "" {
		if !strings.HasPrefix(file, opt.Prefix) {
			return false
		}
		file = file[len(opt.Prefix):]
		if file != "" && file[0] != '/' {
			return false
		}
	}

	f, err := opt.FileSystem.Open(file)
	if err != nil {
		// 404 requests to any known entries in `public`
		if path.Base(opts.Directory) == "public" {
			parts := strings.Split(file, "/")
			if len(parts) < 2 {
				return false
			}
			for _, entry := range KnownPublicEntries {
				if entry == parts[1] {
					w.WriteHeader(404)
					return true
				}
			}
		}
		return false
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		log.Printf("[Static] %q exists, but fails to open: %v", file, err)
		return true
	}

	// Try to serve index file
	if fi.IsDir() {
		// Redirect if missing trailing slash.
		if !strings.HasSuffix(req.URL.Path, "/") {
			http.Redirect(w, req, path.Clean(req.URL.Path+"/"), http.StatusFound)
			return true
		}

		f, err = opt.FileSystem.Open(file)
		if err != nil {
			return false // Discard error.
		}
		defer f.Close()

		fi, err = f.Stat()
		if err != nil || fi.IsDir() {
			return false
		}
	}

	if !opt.SkipLogging {
		log.Println("[Static] Serving " + file)
	}

	// Add an Expires header to the static content
	if opt.ExpiresAfter > 0 {
		w.Header().Set("Expires", time.Now().Add(opt.ExpiresAfter).UTC().Format(http.TimeFormat))
		tag := GenerateETag(fmt.Sprint(fi.Size()), fi.Name(), fi.ModTime().UTC().Format(http.TimeFormat))
		w.Header().Set("ETag", tag)
		if req.Header.Get("If-None-Match") == tag {
			w.WriteHeader(304)
			return true
		}
	}

	http.ServeContent(w, req, file, fi.ModTime(), f)
	return true
}

// GenerateETag generates an ETag based on size, filename and file modification time
func GenerateETag(fileSize, fileName, modTime string) string {
	etag := fileSize + fileName + modTime
	return base64.StdEncoding.EncodeToString([]byte(etag))
}
