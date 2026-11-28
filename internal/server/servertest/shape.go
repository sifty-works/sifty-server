package servertest

import (
	"reflect"
	"strconv"
	"strings"

	"github.com/sifty-works/sifty-server/internal/server"
)

func deref(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t
}

func Shape(typ reflect.Type, body map[string]any, prefix string) []string {
	if typ == nil {
		return nil
	}
	typ = deref(typ)
	if typ.Kind() != reflect.Struct {
		return nil
	}
	var errs []string
	known := make(map[string]bool)
	for i := range typ.NumField() {
		f := typ.Field(i)
		n := server.JSONTagName(f)
		if n == "" || n == "-" {
			continue
		}
		known[n] = true
		p := prefix + n
		val, ok := body[n]
		if !ok {
			if !strings.Contains(f.Tag.Get("json"), "omitempty") {
				errs = append(errs, "required field missing: "+p)
			}
			continue
		}
		if val == nil {
			if f.Type.Kind() != reflect.Ptr {
				errs = append(errs, "unexpected null: "+p)
			}
			continue
		}
		if ft := deref(f.Type); ft.Kind() == reflect.Struct {
			if m, ok := val.(map[string]any); ok {
				errs = append(errs, Shape(ft, m, p+".")...)
			}
		} else if ft.Kind() == reflect.Slice {
			if et := deref(ft.Elem()); et.Kind() == reflect.Struct {
				if vs, ok := val.([]any); ok {
					for j, v := range vs {
						if m, ok := v.(map[string]any); ok {
							errs = append(errs, Shape(et, m, p+"["+strconv.Itoa(j)+"].")...)
						}
					}
				}
			}
		}
	}
	for k := range body {
		if !known[k] {
			errs = append(errs, "unknown field: "+prefix+k)
		}
	}
	return errs
}
