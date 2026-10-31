package config

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// tomlKeys walks the toml tags of t and returns every leaf key path; map keys become <alias>.
func tomlKeys(t reflect.Type, prefix string, out *[]string) {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("toml")
		if tag == "" {
			continue
		}
		path := tag
		if prefix != "" {
			path = prefix + "." + tag
		}
		ft := f.Type
		for ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		switch ft.Kind() {
		case reflect.Struct:
			tomlKeys(ft, path, out)
		case reflect.Map:
			tomlKeys(ft.Elem(), path+".<alias>", out)
		default:
			*out = append(*out, path)
		}
	}
}

func referenceKeys() []string {
	var keys []string
	for _, e := range Reference {
		keys = append(keys, e.Key)
	}
	sort.Strings(keys)
	return keys
}

func codeKeys() []string {
	var keys []string
	tomlKeys(reflect.TypeOf(Config{}), "", &keys)
	sort.Strings(keys)
	return keys
}

func TestReference_CoversEveryTomlKey(t *testing.T) {
	ref := map[string]bool{}
	for _, k := range referenceKeys() {
		ref[k] = true
	}
	for _, k := range codeKeys() {
		if !ref[k] {
			t.Errorf("config key %q is missing from the reference", k)
		}
	}
}

func TestReference_HasNoUnknownKeys(t *testing.T) {
	code := map[string]bool{}
	for _, k := range codeKeys() {
		code[k] = true
	}
	for _, k := range referenceKeys() {
		if !code[k] {
			t.Errorf("reference key %q is not a config key", k)
		}
	}
}

func TestReference_EntriesComplete(t *testing.T) {
	if len(Reference) == 0 {
		t.Fatal("reference is empty")
	}
	for _, e := range Reference {
		if e.Key == "" || strings.TrimSpace(e.Type) == "" || strings.TrimSpace(e.Default) == "" ||
			strings.TrimSpace(e.Example) == "" || strings.TrimSpace(e.Description) == "" {
			t.Errorf("reference entry %q has an empty field: %+v", e.Key, e)
		}
	}
}

func TestFormatReference_ListsEveryKey(t *testing.T) {
	out := FormatReference()
	for _, e := range Reference {
		if !strings.Contains(out, e.Key) {
			t.Errorf("formatted reference lacks %q", e.Key)
		}
	}
}
