package testutil

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
)

func NewTestServer(baseDir string) *httptest.Server {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rootDir, err := os.OpenRoot(baseDir)
		if err != nil {
			http.Error(w, "internal server error", 500)
			return
		}
		defer rootDir.Close()
		f, err := rootDir.Open(r.URL.Path)
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		buf, err := io.ReadAll(f)
		if err != nil {
			http.Error(w, "internal server error", 500)
			return
		}
		w.Write(buf)
	}))
	return ts
}
