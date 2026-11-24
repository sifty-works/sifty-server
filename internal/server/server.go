package server

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sifty-works/sifty-server/internal/apperr"
	"github.com/sifty-works/sifty-server/internal/models"
)

type CollectProgress struct {
	Done  int    `json:"done"`
	Total int    `json:"total"`
	Text  string `json:"text"`
}

type LastRun struct {
	State    string `json:"state"`
	Finished string `json:"finished"`
	Message  string `json:"message"`
}

type Attention struct {
	Count int    `json:"count"`
	Text  string `json:"text"`
}

type StatusExtra struct {
	Collecting *CollectProgress `json:"collecting"`
	LastRun    *LastRun         `json:"lastRun"`
	Catalogue  int64            `json:"catalogue"`
	Attention  *Attention       `json:"attention"`
}

type RecoveredFile struct {
	File string `json:"file"`
	Text string `json:"text"`
}

type StatusResp struct {
	Version    string               `json:"version"`
	Offline    bool                 `json:"offline"`
	Debug      bool                 `json:"debug"`
	Collecting *CollectProgress     `json:"collecting"`
	LastRun    *LastRun             `json:"lastRun"`
	Catalogue  int64                `json:"catalogue"`
	Attention  *Attention           `json:"attention"`
	Models     *models.ModelsStatus `json:"models,omitempty"`
	Recovered  []RecoveredFile      `json:"recovered"`
}

type VersionResp struct {
	Version string `json:"version"`
	API     string `json:"api"`
}

const (
	maxBodyBytes       = 1 << 20
	maxUploadBodyBytes = 2 << 20
	slowThreshold      = 500 * time.Millisecond
)

var counter atomic.Uint64

func init() {
	counter.Store(uint64(time.Now().Unix()))
}

type Options struct {
	Version           string
	APISha256         string
	Port              int
	Offline           bool
	Debug             bool
	Logger            *slog.Logger
	Static            fs.FS
	Status            func() StatusExtra
	ModelsStatus      func() *models.ModelsStatus
	BinaryUploadPaths []string
	Recovered         []RecoveredFile
}

func New(opts Options, routes func(*http.ServeMux)) http.Handler {
	mux := http.NewServeMux()

	Handle(mux, "GET /api/status", opts.Logger, func(r *http.Request, _ struct{}) (StatusResp, error) {
		resp := StatusResp{Version: opts.Version, Offline: opts.Offline, Debug: opts.Debug, Recovered: opts.Recovered}
		if opts.Status != nil {
			extra := opts.Status()
			resp.Collecting = extra.Collecting
			resp.LastRun = extra.LastRun
			resp.Catalogue = extra.Catalogue
			resp.Attention = extra.Attention
		}
		if opts.ModelsStatus != nil {
			resp.Models = opts.ModelsStatus()
		}
		return resp, nil
	})

	Handle(mux, "GET /api/version", opts.Logger, func(r *http.Request, _ struct{}) (VersionResp, error) {
		return VersionResp{Version: opts.Version, API: opts.APISha256}, nil
	})

	if routes != nil {
		routes(mux)
	}

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound,
			"Refresh the page. Sifty was updated and this address is no longer used.")
	})

	mux.Handle("/", staticHandler(opts.Static))

	return chain(mux, opts)
}

func chain(h http.Handler, opts Options) http.Handler {
	uploadSet := make(map[string]bool, len(opts.BinaryUploadPaths))
	for _, p := range opts.BinaryUploadPaths {
		uploadSet[p] = true
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		if !allowedHost(r.Host, opts.Port) {
			writeError(w, http.StatusForbidden,
				"This address may only be opened from this computer.")
			return
		}

		if origin := r.Header.Get("Origin"); origin != "" {
			if !allowedOrigin(origin, opts.Port) {
				writeError(w, http.StatusForbidden,
					"This address may only be opened from this computer.")
				return
			}
		}

		isBinaryUpload := isMutating(r.Method) && uploadSet[r.URL.Path]

		if isMutating(r.Method) && !isBinaryUpload {
			ct := r.Header.Get("Content-Type")
			if !strings.HasPrefix(ct, "application/json") {
				writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json.")
				return
			}
		}

		bodyLimit := int64(maxBodyBytes)
		if isBinaryUpload {
			bodyLimit = maxUploadBodyBytes
		}
		if r.ContentLength > bodyLimit {
			writeError(w, http.StatusRequestEntityTooLarge, "Request body too large.")
			return
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)
		}

		rw := &responseWriter{ResponseWriter: w}
		defer func() {
			if rec := recover(); rec != nil {
				id := counter.Add(1)
				opts.Logger.Error("panic in handler",
					"id", id,
					"method", r.Method,
					"path", r.URL.Path,
					"panic", fmt.Sprintf("%v", rec),
				)
				writeError(rw, http.StatusInternalServerError,
					fmt.Sprintf("Internal error, see log #%d", id))
			}
		}()

		h.ServeHTTP(rw, r)

		elapsed := time.Since(start)
		if elapsed >= slowThreshold {
			opts.Logger.Warn("slow request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rw.written(),
				"duration", elapsed,
			)
		}
	})
}

func allowedHost(host string, port int) bool {
	portStr := fmt.Sprintf("%d", port)
	return host == "127.0.0.1:"+portStr || host == "localhost:"+portStr
}

func allowedOrigin(origin string, port int) bool {
	portStr := fmt.Sprintf("%d", port)
	return origin == "http://127.0.0.1:"+portStr || origin == "http://localhost:"+portStr
}

func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func staticHandler(fsys fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fsys == nil {
			http.Error(w, "The frontend was not built. Run make build.", http.StatusOK)
			return
		}

		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		f, err := fsys.Open(path)
		if err == nil {
			f.Close()
			http.FileServerFS(fsys).ServeHTTP(w, r)
			return
		}

		idx, err := fsys.Open("index.html")
		if err != nil {
			http.Error(w, "The frontend was not built. Run make build.", http.StatusOK)
			return
		}
		defer idx.Close()
		content, _ := io.ReadAll(idx)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write(content)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func MapError(w http.ResponseWriter, err error, logger *slog.Logger) bool {
	if err == nil {
		return false
	}
	switch e := err.(type) {
	case apperr.Invalid:
		writeError(w, http.StatusUnprocessableEntity, e.Message)
	case apperr.NotFound:
		writeError(w, http.StatusNotFound, e.Message)
	case apperr.Conflict:
		writeError(w, http.StatusConflict, e.Message)
	case apperr.Busy:
		writeError(w, http.StatusLocked, e.Message)
	default:
		id := counter.Add(1)
		logger.Error("internal error", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError,
			fmt.Sprintf("Internal error, see log #%d", id))
	}
	return true
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(status int) {
	rw.status = status
	rw.ResponseWriter.WriteHeader(status)
}

func (rw *responseWriter) written() int {
	if rw.status == 0 {
		return http.StatusOK
	}
	return rw.status
}
