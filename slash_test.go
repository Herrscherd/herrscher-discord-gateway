package discord

import (
	"reflect"
	"testing"

	"github.com/Herrscherd/dctl"
)

// route walks groups + sub-commands and returns the command path plus the leaf
// options of the deepest sub. This is the crux of keeping the core neutral: the
// path becomes the argv the SessionControl seam dispatches.
func TestRouteSubcommand(t *testing.T) {
	d := dctl.InteractionData{
		Name: "set",
		Options: []dctl.InteractionOption{{
			Name: "home", Type: 1,
			Options: []dctl.InteractionOption{{Name: "channel", Type: 7, Value: "cat1"}},
		}},
	}
	path, leaves := route(d)
	if !reflect.DeepEqual(path, []string{"set", "home"}) {
		t.Fatalf("path = %v", path)
	}
	if len(leaves) != 1 || leaves[0].Name != "channel" {
		t.Fatalf("leaves = %+v", leaves)
	}
}

func TestRouteGroup(t *testing.T) {
	d := dctl.InteractionData{
		Name: "session",
		Options: []dctl.InteractionOption{{
			Name: "allow", Type: 2,
			Options: []dctl.InteractionOption{{
				Name: "add", Type: 1,
				Options: []dctl.InteractionOption{
					{Name: "name", Type: 3, Value: "demo"},
					{Name: "user", Type: 6, Value: "u1"},
				},
			}},
		}},
	}
	path, leaves := route(d)
	if !reflect.DeepEqual(path, []string{"session", "allow", "add"}) {
		t.Fatalf("path = %v", path)
	}
	if len(leaves) != 2 {
		t.Fatalf("leaves = %+v", leaves)
	}
}

// flagsFrom turns leaf options into the `--name value` / bare `--flag` argv the
// core CLI registry parses; a false bool emits nothing.
func TestFlagsFrom(t *testing.T) {
	leaves := []dctl.InteractionOption{
		{Name: "name", Type: 3, Value: "demo"},
		{Name: "shared", Type: 5, Value: true},
		{Name: "force", Type: 5, Value: false},
		{Name: "backend", Type: 3, Value: "oneshot"},
	}
	got := flagsFrom(leaves)
	want := []string{"--name", "demo", "--shared", "--backend", "oneshot"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("flagsFrom = %v, want %v", got, want)
	}
}

func TestValStr(t *testing.T) {
	if valStr("x") != "x" || valStr(true) != "true" || valStr(float64(42)) != "42" {
		t.Fatalf("valStr mismatch")
	}
}

func TestListUsers(t *testing.T) {
	if got := listUsers("allowlist", nil); got != "allowlist: empty (everyone allowed)" {
		t.Fatalf("empty list = %q", got)
	}
	if got := listUsers("allowlist", []string{"a", "b"}); got != "allowlist: <@a>, <@b>" {
		t.Fatalf("list = %q", got)
	}
}
