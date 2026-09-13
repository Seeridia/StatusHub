package controlplane

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	apiContract "github.com/Seeridia/StatusHub/api"
)

//go:embed assets/*
var assets embed.FS

func (s *Server) handleOpenAPI(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Content-Type", "application/yaml")
	response.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = response.Write(apiContract.OpenAPI)
}

func (s *Server) handleUI(response http.ResponseWriter, request *http.Request) {
	name := strings.TrimPrefix(request.URL.Path, "/ui/")
	if name == "" {
		name = "index.html"
	}
	if !fs.ValidPath(name) {
		http.NotFound(response, request)
		return
	}
	// Keep the previous console reachable during migration; the default is React.
	root := "assets/console/"
	if name == "legacy.html" {
		root = "assets/"
		name = "index.html"
	}
	if name == "app.js" || name == "styles.css" {
		root = "assets/"
	}
	data, err := assets.ReadFile(root + name)
	if err != nil {
		http.NotFound(response, request)
		return
	}
	contentType := mime.TypeByExtension(path.Ext(name))
	if contentType == "" {
		http.NotFound(response, request)
		return
	}
	response.Header().Set("Content-Type", contentType)
	response.Header().Set("Cache-Control", "no-cache")
	if strings.HasPrefix(name, "assets/") {
		response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	if name == "index.html" && root == "assets/console/" {
		// TDesign positions popups with style attributes; scripts and stylesheets
		// remain same-origin only. Icon CSS and modal scroll locking are external.
		response.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self'; style-src-attr 'unsafe-inline'; script-src 'self'; base-uri 'none'; frame-ancestors 'none'")
	}
	if path.Ext(name) == ".js" || path.Ext(name) == ".css" {
		response.Header().Set("Vary", "Accept-Encoding")
		if acceptsGzip(request.Header.Get("Accept-Encoding")) {
			if compressed, err := assets.ReadFile(root + name + ".gz"); err == nil {
				response.Header().Set("Content-Encoding", "gzip")
				data = compressed
			}
		}
	}
	_, _ = response.Write(data)
}

func acceptsGzip(value string) bool {
	for _, coding := range strings.Split(value, ",") {
		parts := strings.Split(strings.TrimSpace(coding), ";")
		if !strings.EqualFold(parts[0], "gzip") {
			continue
		}
		for _, parameter := range parts[1:] {
			key, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if ok && key == "q" {
				quality, err := strconv.ParseFloat(value, 64)
				return err == nil && quality > 0 && quality <= 1
			}
		}
		return true
	}
	return false
}
