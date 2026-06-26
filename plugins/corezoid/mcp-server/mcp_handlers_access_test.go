package main

import (
	"strings"
	"testing"
)

// ---- privsExplicitNone -----------------------------------------------------

func TestPrivsExplicitNone(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"   ", false},
		{"none", true},
		{"NONE", true},
		{"  none  ", true},
		{"[]", true},
		{"revoke", true},
		{"unshare", true},
		{"view", false},
		{"all", false},
		{"view,modify", false},
	}
	for _, c := range cases {
		if got := privsExplicitNone(c.in); got != c.want {
			t.Errorf("privsExplicitNone(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// ---- parsePrivs ------------------------------------------------------------

func TestParsePrivs_Empty(t *testing.T) {
	out, err := parsePrivs("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != nil {
		t.Errorf("expected nil for empty input, got %v", out)
	}
}

func TestParsePrivs_All(t *testing.T) {
	out, err := parsePrivs("all")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != len(AllPrivs) {
		t.Errorf("expected full priv bundle, got %v", out)
	}
}

func TestParsePrivs_CommaSeparated(t *testing.T) {
	out, err := parsePrivs("view, modify")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 2 || out[0] != PrivView || out[1] != PrivModify {
		t.Errorf("unexpected result: %v", out)
	}
}

func TestParsePrivs_JSONArray(t *testing.T) {
	out, err := parsePrivs(`["view","create","delete"]`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("expected 3 privs, got %d", len(out))
	}
	if out[0] != PrivView || out[1] != PrivCreate || out[2] != PrivDelete {
		t.Errorf("unexpected ordering: %v", out)
	}
}

func TestParsePrivs_JSONArrayInvalid(t *testing.T) {
	_, err := parsePrivs(`[not json`)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParsePrivs_UnknownPriv(t *testing.T) {
	_, err := parsePrivs("view,wild")
	if err == nil {
		t.Error("expected error for unknown priv")
	}
}

func TestParsePrivs_CaseInsensitive(t *testing.T) {
	out, err := parsePrivs("VIEW,Modify")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 2 || out[0] != PrivView || out[1] != PrivModify {
		t.Errorf("unexpected result: %v", out)
	}
}

func TestParsePrivs_AllInJSONArray(t *testing.T) {
	out, err := parsePrivs(`["view","all"]`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// "all" wins and replaces accumulated tokens.
	if len(out) != len(AllPrivs) {
		t.Errorf("expected full bundle when 'all' appears, got %v", out)
	}
}

// ---- privsString -----------------------------------------------------------

func TestPrivsString(t *testing.T) {
	if got := privsString(nil); got != "" {
		t.Errorf("expected empty for nil, got %q", got)
	}
	got := privsString([]PrivType{PrivView, PrivModify})
	if got != "view,modify" {
		t.Errorf("expected \"view,modify\", got %q", got)
	}
}

// ---- truncate --------------------------------------------------------------

func TestTruncate(t *testing.T) {
	cases := []struct {
		s    string
		max  int
		want string
	}{
		{"hello", 10, "hello"}, // shorter than max
		{"hello", 5, "hello"},  // equal to max
		{"hello world", 8, "hello w…"},
		{"abc", 1, "a"},      // max==1 uses raw slice
		{"abc", 0, "abc"},    // max<=0 returns input as-is
		{"abc", -3, "abc"},   // negative max returns input
		{"", 5, ""},
	}
	for _, c := range cases {
		if got := truncate(c.s, c.max); got != c.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.s, c.max, got, c.want)
		}
	}
}

// ---- formatItemPrivs -------------------------------------------------------

func TestFormatItemPrivs(t *testing.T) {
	item := map[string]any{
		"privs": []any{
			map[string]any{"type": "view"},
			map[string]any{"type": "modify"},
		},
	}
	if got := formatItemPrivs(item); got != "view,modify" {
		t.Errorf("expected \"view,modify\", got %q", got)
	}
}

func TestFormatItemPrivs_EmptyOrMissing(t *testing.T) {
	if got := formatItemPrivs(map[string]any{}); got != "" {
		t.Errorf("expected empty for missing field, got %q", got)
	}
	// Field present but wrong type.
	if got := formatItemPrivs(map[string]any{"privs": "view"}); got != "" {
		t.Errorf("expected empty for non-slice privs, got %q", got)
	}
}

func TestFormatItemPrivs_SkipsBadEntries(t *testing.T) {
	item := map[string]any{
		"privs": []any{
			map[string]any{"type": "view"},
			"not-a-map",
			map[string]any{}, // missing type
			map[string]any{"type": "delete"},
		},
	}
	got := formatItemPrivs(item)
	// Only entries with a non-empty type field contribute.
	if !strings.Contains(got, "view") || !strings.Contains(got, "delete") {
		t.Errorf("expected view and delete, got %q", got)
	}
	if parts := strings.Split(got, ","); len(parts) != 2 {
		t.Errorf("expected 2 entries, got %q", got)
	}
}
