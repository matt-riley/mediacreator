package main

import (
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFetchModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != marketPath {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200, "msg": "success",
			"data": []any{"z-image", "bytedance/seedream", "veo3"},
		})
	}))
	defer srv.Close()

	got, err := fetchModels(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"bytedance/seedream", "veo3", "z-image"} // sorted
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestFetchModelsServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"code":500}`, 500)
	}))
	defer srv.Close()
	if _, err := fetchModels(context.Background(), srv.URL); err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestRender(t *testing.T) {
	src := render([]string{"b/2", "a/1"})
	// Must be valid Go that parses as a package.
	if _, err := parser.ParseFile(token.NewFileSet(), "kie_catalog.go", src, parser.AllErrors); err != nil {
		t.Fatalf("render produced invalid Go: %v\n%s", err, src)
	}
	for _, want := range []string{`"a/1"`, `"b/2"`, "var kieFallbackCatalog = []string{", "DO NOT EDIT"} {
		if !strings.Contains(src, want) {
			t.Errorf("render output missing %q:\n%s", want, src)
		}
	}
}

func TestDefaultOut(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	// From the cmd/gen-kie-catalog directory, defaultOut must point at the
	// module's catalog file (via the relative path).
	if err := os.Chdir(filepath.Join(wd, "..", "..", "internal", "provider")); err != nil {
		t.Fatal(err)
	}
	if got := defaultOut(); got != "kie_catalog.go" {
		t.Fatalf("defaultOut from provider dir = %q", got)
	}
}
