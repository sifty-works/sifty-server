package server

import (
	"encoding/json"
	"log/slog"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/sifty-works/sifty-server/internal/apperr"
)

type RouteInfo struct {
	Pattern  string
	ReqType  reflect.Type
	RespType reflect.Type
}

var (
	routeMu  sync.Mutex
	routeMap = map[string]RouteInfo{}
)

func Routes() []RouteInfo {
	routeMu.Lock()
	defer routeMu.Unlock()
	out := slices.Collect(maps.Values(routeMap))
	slices.SortFunc(out, func(a, b RouteInfo) int { return strings.Compare(a.Pattern, b.Pattern) })
	return out
}

func ResetRoutes() {
	routeMu.Lock()
	defer routeMu.Unlock()
	routeMap = map[string]RouteInfo{}
}

func normType[T any]() (t reflect.Type) {
	if t = reflect.TypeOf(*new(T)); t != nil && t.Kind() == reflect.Struct && t.NumField() == 0 {
		t = nil
	}
	return
}

func HandleRaw(mux *http.ServeMux, pattern string, fn http.HandlerFunc) {
	routeMu.Lock()
	routeMap[pattern] = RouteInfo{Pattern: pattern}
	routeMu.Unlock()
	mux.HandleFunc(pattern, fn)
}

func Handle[Req, Resp any](mux *http.ServeMux, pattern string, log *slog.Logger, fn func(*http.Request, Req) (Resp, error)) {
	reqType, rspType := normType[Req](), normType[Resp]()
	routeMu.Lock()
	routeMap[pattern] = RouteInfo{Pattern: pattern, ReqType: reqType, RespType: rspType}
	routeMu.Unlock()
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		req, err := decodeReq[Req](pattern, r)
		if MapError(w, err, log) {
			return
		}
		resp, err := fn(r, req)
		if MapError(w, err, log) {
			return
		}
		if rspType == nil {
			w.WriteHeader(http.StatusNoContent)
		} else {
			writeJSON(w, http.StatusOK, resp)
		}
	})
}

func decodeReq[T any](pattern string, r *http.Request) (T, error) {
	if strings.HasPrefix(pattern, "GET ") {
		return decodeQuery[T](r)
	}
	return decodeBody[T](r)
}

func decodeQuery[T any](r *http.Request) (T, error) {
	var v T
	rv := reflect.ValueOf(&v).Elem()
	if rv.Kind() != reflect.Struct {
		return v, nil
	}
	q, t := r.URL.Query(), rv.Type()
	for i := range t.NumField() {
		f := t.Field(i)
		name := JSONTagName(f)
		if name == "" || name == "-" {
			continue
		}
		s := q.Get(name)
		if s == "" {
			continue
		}
		fv := rv.Field(i)
		switch fv.Kind() {
		case reflect.String:
			fv.SetString(s)
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			n, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return v, apperr.Invalid{Message: "Invalid " + name + "."}
			}
			fv.SetInt(n)
		case reflect.Bool:
			b, err := strconv.ParseBool(s)
			if err != nil {
				return v, apperr.Invalid{Message: "Invalid " + name + "."}
			}
			fv.SetBool(b)
		}
	}
	return v, nil
}

func decodeBody[T any](r *http.Request) (T, error) {
	var v T
	if r.Body == nil || r.ContentLength == 0 {
		return v, nil
	}
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		return v, apperr.Invalid{Message: "Invalid request body."}
	}
	return v, nil
}

func JSONTagName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "" {
		return strings.ToLower(f.Name)
	}
	name, _, _ := strings.Cut(tag, ",")
	return name
}
