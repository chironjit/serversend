package main

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// The native executable ships these bytes, rather than the source frontend.
// Catch missing build artifacts and broken asset URLs at the embed boundary.
func TestEmbeddedFrontendAssets(t *testing.T) {
	frontend, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		t.Fatal(err)
	}
	index, err := fs.ReadFile(frontend, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), `id="app"`) {
		t.Fatal("embedded frontend has no application mount")
	}
	urls := regexp.MustCompile(`(?:src|href)="(/[^"#?]+)"`).FindAllSubmatch(index, -1)
	if len(urls) == 0 {
		t.Fatal("embedded frontend has no asset references")
	}
	var bundledJS bool
	for _, match := range urls {
		path := strings.TrimPrefix(string(match[1]), "/")
		if strings.HasPrefix(path, "src/") {
			t.Fatalf("unbuilt source reference in embedded frontend: %s", path)
		}
		data, err := fs.ReadFile(frontend, path)
		if err != nil || len(data) == 0 {
			t.Fatalf("embedded asset %s is unavailable: %v", path, err)
		}
		bundledJS = bundledJS || strings.HasPrefix(path, "assets/") && strings.HasSuffix(path, ".js")
	}
	if !bundledJS {
		t.Fatal("embedded frontend has no compiled JavaScript bundle")
	}
}
