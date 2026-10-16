package main

import (
	"cmp"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const maxFeedBytes = 32 << 20

type server struct {
	client *http.Client
}

func main() {
	addr := ":" + cmp.Or(os.Getenv("PORT"), "8080")
	s := &server{client: &http.Client{Timeout: 30 * time.Second}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /feed", s.feed)
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /preview", s.preview)
	mux.HandleFunc("GET /htmx.min.js", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, webFS, "web/htmx.min.js")
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {})

	slog.Info("listening", "addr", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func (s *server) feed(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	baseURL, sourceURL := q.Get("base"), q.Get("source")
	if baseURL == "" || sourceURL == "" {
		http.Error(w, "base and source are required", http.StatusBadRequest)
		return
	}
	o, err := parseOptions(q)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	base, err := s.fetch(r, baseURL)
	if err != nil {
		s.fail(w, err)
		return
	}
	var source []byte
	source, err = s.fetch(r, sourceURL)
	if err != nil {
		s.fail(w, err)
		return
	}
	var out []byte
	out, err = patch(base, source, o)
	if err != nil {
		s.fail(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	w.Write(out)
}

func parseOptions(q url.Values) (options, error) {
	ex, err := parseExtractors(q["extract"])
	return options{
		match:   parseRules(q["match"]),
		extract: ex,
		replace: splitFields(cmp.Or(strings.Join(q["replace"], ","), defaultReplace)),
	}, err
}

func (s *server) fetch(r *http.Request, url string) ([]byte, error) {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("unsupported url scheme: %q", url)
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: status %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxFeedBytes))
}

func (s *server) fail(w http.ResponseWriter, err error) {
	slog.Error("request failed", "err", err)
	http.Error(w, err.Error(), http.StatusBadGateway)
}
