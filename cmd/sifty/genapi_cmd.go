package main

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"reflect"
	"strings"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/collect"
	"github.com/sifty-works/sifty-server/internal/server"
	"gopkg.in/yaml.v3"
)

var typeEnums = map[string][]string{
	"Stage":         {"inbox", "shortlist", "applied", "dismissed"},
	"ConfirmDialog": {"outcome", "delete"},
	"ExclusionKind": {"word", "company"},
}

func cmdGenAPI(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("gen-api", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "api/openapi.yaml", "output file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	server.ResetRoutes()
	devNull := slog.New(slog.NewTextHandler(io.Discard, nil))
	devColl := collect.New(nil, nil, nil, nil, nil, false, nil, devNull)
	server.New(server.Options{Logger: devNull}, func(mux *http.ServeMux) {
		registerAllRoutes(mux, devNull, nil, nil, nil, nil, nil, nil, nil, "", candidate.Readers{}, devColl, nil, nil, "", nil)
	})
	routes := server.Routes()
	data, err := genDoc(routes)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, data, 0600); err != nil {
		return fmt.Errorf("gen-api: write: %w", err)
	}
	fmt.Fprintf(stdout, "wrote %s (%d routes)\n", *out, len(routes))
	return nil
}

func genDoc(routes []server.RouteInfo) ([]byte, error) {
	schemas, paths := map[string]any{}, map[string]any{}
	for _, ri := range routes {
		m, path, _ := strings.Cut(ri.Pattern, " ")
		if _, ok := paths[path]; !ok {
			paths[path] = map[string]any{}
		}
		parts := []string{strings.ToLower(m)}
		for _, seg := range strings.Split(strings.TrimPrefix(ri.Pattern[len(m)+1:], "/"), "/") {
			seg = strings.Trim(seg, "{}")
			if seg != "" {
				parts = append(parts, strings.ToUpper(seg[:1])+seg[1:])
			}
		}
		op := map[string]any{"operationId": strings.Join(parts, "")}
		if ri.ReqType != nil {
			if m == "GET" {
				var params []any
				for i := range ri.ReqType.NumField() {
					f := ri.ReqType.Field(i)
					if n := server.JSONTagName(f); n != "" && n != "-" {
						p := map[string]any{"in": "query", "name": n, "schema": genSchema(f.Type, schemas)}
						if !strings.Contains(f.Tag.Get("json"), "omitempty") {
							p["required"] = true
						}
						params = append(params, p)
					}
				}
				if len(params) > 0 {
					op["parameters"] = params
				}
			} else {
				op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": genSchema(ri.ReqType, schemas)}}}
			}
		}
		resp := map[string]any{"4XX": map[string]any{"$ref": "#/components/responses/Error"}}
		if ri.RespType != nil {
			resp["200"] = map[string]any{"description": "OK", "content": map[string]any{"application/json": map[string]any{"schema": genSchema(ri.RespType, schemas)}}}
		} else {
			resp["204"] = map[string]any{"description": "No Content"}
		}
		op["responses"] = resp
		paths[path].(map[string]any)[strings.ToLower(m)] = op
	}
	doc := map[string]any{
		"openapi": "3.1.0",
		"info":    map[string]any{"title": "Sifty API", "version": "dev"},
		"paths":   paths,
		"components": map[string]any{
			"responses": map[string]any{"Error": map[string]any{"description": "Error", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "required": []string{"error"}, "properties": map[string]any{"error": map[string]any{"type": "string"}}}}}}},
			"schemas":   schemas,
		},
	}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("gen-api: marshal: %w", err)
	}
	return data, nil
}

func genSchema(t reflect.Type, out map[string]any) map[string]any {
	if t.Kind() == reflect.Ptr {
		return map[string]any{"oneOf": []any{genSchema(t.Elem(), out), map[string]any{"type": "null"}}}
	}
	if t.Kind() == reflect.Slice {
		return map[string]any{"type": "array", "items": genSchema(t.Elem(), out)}
	}
	if t.Kind() == reflect.Map {
		return map[string]any{"type": "object", "additionalProperties": genSchema(t.Elem(), out)}
	}
	if t.Kind() == reflect.Struct {
		name := t.Name()
		if out[name] == nil {
			props, req := map[string]any{}, []string(nil)
			for i := range t.NumField() {
				f := t.Field(i)
				if f.Anonymous && f.Tag.Get("json") == "" {
					if f.Type.Kind() == reflect.Struct {
						for j := range f.Type.NumField() {
							ef := f.Type.Field(j)
							if n := server.JSONTagName(ef); n != "" && n != "-" {
								if !strings.Contains(ef.Tag.Get("json"), "omitempty") {
									req = append(req, n)
								}
								props[n] = genSchema(ef.Type, out)
							}
						}
					}
					continue
				}
				if n := server.JSONTagName(f); n != "" && n != "-" {
					if !strings.Contains(f.Tag.Get("json"), "omitempty") {
						req = append(req, n)
					}
					props[n] = genSchema(f.Type, out)
				}
			}
			s := map[string]any{"type": "object", "properties": props}
			if len(req) > 0 {
				s["required"] = req
			}
			out[name] = s
		}
		return map[string]any{"$ref": "#/components/schemas/" + name}
	}
	switch t.Kind() {
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer", "format": "int64"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number", "format": "double"}
	}
	s := map[string]any{"type": "string"}
	if vals := typeEnums[t.Name()]; len(vals) > 0 {
		s["enum"] = vals
	}
	return s
}
