package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/sifty-works/sifty-server/internal/server"
	"github.com/sifty-works/sifty-server/internal/server/servertest"
)

func TestContractResponseShapes(t *testing.T) {
	dir := t.TempDir()
	port, cancel := startServer(t, dir, true)
	defer cancel()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	cl := http.Client{}

	for _, ri := range server.Routes() {
		ri := ri
		if !strings.HasPrefix(ri.Pattern, "GET ") {
			continue
		}
		path := strings.TrimPrefix(ri.Pattern, "GET ")
		if strings.Contains(path, "{") {
			continue
		}
		if ri.RespType == nil {
			continue
		}

		t.Run(ri.Pattern, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, base+path, nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			resp, err := cl.Do(req)
			if err != nil {
				t.Fatalf("GET %s: %v", path, err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s: status %d, want 200", path, resp.StatusCode)
			}

			var body map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("GET %s: decode body: %v", path, err)
			}

			for _, violation := range servertest.Shape(ri.RespType, body, "") {
				t.Errorf("GET %s: shape: %s", path, violation)
			}
		})
	}
}

func TestContractSchemaMatchesShape(t *testing.T) {
	dir := t.TempDir()
	_, cancel := startServer(t, dir, true)
	defer cancel()

	schemas := map[string]any{}
	for _, ri := range server.Routes() {
		if ri.RespType != nil {
			genSchema(ri.RespType, schemas)
		}
	}

	for _, ri := range server.Routes() {
		if ri.RespType == nil {
			continue
		}
		t.Run(ri.Pattern, func(t *testing.T) {
			name := ri.RespType.Name()
			s, _ := schemas[name].(map[string]any)
			props, _ := s["properties"].(map[string]any)
			if props == nil {
				return
			}
			shapeSet := make(map[string]bool)
			for _, n := range topLevelFieldNames(ri.RespType) {
				shapeSet[n] = true
			}
			for k := range props {
				if !shapeSet[k] {
					t.Errorf("schema property %q is absent from Go shape", k)
				}
			}
			for n := range shapeSet {
				if _, ok := props[n]; !ok {
					t.Errorf("Go shape field %q is absent from schema", n)
				}
			}
		})
	}
}

func topLevelFieldNames(typ reflect.Type) []string {
	if typ == nil {
		return nil
	}
	t := typ
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	var names []string
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Anonymous && f.Tag.Get("json") == "" {
			names = append(names, topLevelFieldNames(f.Type)...)
		} else if n := server.JSONTagName(f); n != "" && n != "-" {
			names = append(names, n)
		}
	}
	return names
}
