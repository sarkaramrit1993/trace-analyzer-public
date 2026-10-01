package models

import (
	"reflect"
	"strings"
	"testing"
)

func TestDefaultGroupingTagKeys_Neutral(t *testing.T) {
	k := DefaultGroupingTagKeys()
	if want := []string{"scope1", "scope_1", "scope-1"}; !reflect.DeepEqual(k.Scope1, want) {
		t.Errorf("Scope1 = %v, want %v", k.Scope1, want)
	}
	lists := [][]string{k.ServiceIdentity, k.Scope1, k.Scope2, k.Scope3, k.Env, k.FeatureGroup, k.FeatureName, k.SubService}
	for _, list := range lists {
		if len(list) == 0 {
			t.Errorf("empty default key list in %+v", k)
		}
		for _, key := range list {
			// The Istio canonical service label is a public, vendor-neutral key.
			if strings.Contains(key, ".") && key != "istio.canonical_service" {
				t.Errorf("default key %q contains a '.'", key)
			}
		}
	}
}

func TestGroupingTagKeys_WithDefaultsFillsEmptyLists(t *testing.T) {
	k := GroupingTagKeys{Scope1: []string{"team.scope1"}}.WithDefaults()
	d := DefaultGroupingTagKeys()
	if !reflect.DeepEqual(k.Scope1, []string{"team.scope1"}) {
		t.Errorf("Scope1 = %v, want override kept", k.Scope1)
	}
	if !reflect.DeepEqual(k.Scope2, d.Scope2) || !reflect.DeepEqual(k.ServiceIdentity, d.ServiceIdentity) {
		t.Errorf("empty lists not defaulted: %+v", k)
	}
}

func TestLookup(t *testing.T) {
	tags := map[string]string{"a": "", "b": "2", "c": "3"}
	if got := Lookup(tags, []string{"a", "b", "c"}); got != "2" {
		t.Errorf("Lookup = %q, want first non-empty %q", got, "2")
	}
	if got := Lookup(tags, []string{"x"}); got != "" {
		t.Errorf("Lookup missing = %q, want empty", got)
	}
	if got := Lookup(nil, []string{"a"}); got != "" {
		t.Errorf("Lookup nil tags = %q, want empty", got)
	}
}

func TestDefaultConfig_SetsGroupingTagKeys(t *testing.T) {
	if !reflect.DeepEqual(DefaultConfig().GroupingTagKeys, DefaultGroupingTagKeys()) {
		t.Error("DefaultConfig().GroupingTagKeys != DefaultGroupingTagKeys()")
	}
}
