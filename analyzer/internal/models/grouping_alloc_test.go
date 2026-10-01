package models

import (
	"reflect"
	"testing"
)

func TestDefaultGroupingTagKeys_NoAllocAndAppendSafe(t *testing.T) {
	if n := testing.AllocsPerRun(100, func() { _ = DefaultGroupingTagKeys().WithDefaults() }); n != 0 {
		t.Errorf("DefaultGroupingTagKeys().WithDefaults() allocates %v times", n)
	}
	before := DefaultGroupingTagKeys()
	k := DefaultGroupingTagKeys()
	_ = append(k.Scope1, "extra")
	if !reflect.DeepEqual(DefaultGroupingTagKeys(), before) {
		t.Error("append to a returned list changed the defaults")
	}
}
