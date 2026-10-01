package analysis

import (
	"reflect"
	"sort"
	"testing"
)

func TestParseCombination(t *testing.T) {
	v, n, err := ParseCombination(" ewma:500 ")
	if err != nil || v != VariantEWMA || n != 500 {
		t.Fatalf("ParseCombination(ewma:500) = %v %d %v", v, n, err)
	}
	for _, bad := range []string{"", "ewma", "bogus:50", "ewma:42", "ewma:x", "ewma:50:1"} {
		if _, _, err := ParseCombination(bad); err == nil {
			t.Errorf("ParseCombination(%q) accepted", bad)
		}
	}
}

func TestParseLearnCombinations(t *testing.T) {
	cases := []struct {
		in      string
		active  VariantCombination
		want    []VariantCombination
		wantErr bool
	}{
		{"", "cumulative:50", nil, false},
		{"all", "cumulative:50", nil, false},
		{" ALL ", "cumulative:50", nil, false},
		{"active", "cumulative:50", []VariantCombination{"cumulative:50"}, false},
		{"active", "ewma:500", []VariantCombination{"ewma:500"}, false},
		{"active", "ewma:42", nil, true},
		{"cumulative:50, ewma:500", "cumulative:50", []VariantCombination{"cumulative:50", "ewma:500"}, false},
		{"ewma:500,cumulative:50,ewma:500, ", "cumulative:50", []VariantCombination{"ewma:500", "cumulative:50"}, false},
		{"bogus:7", "cumulative:50", nil, true},
		{"ewma:42", "cumulative:50", nil, true},
		{"cumulative:50,ewma:42", "cumulative:50", nil, true},
	}
	for _, tc := range cases {
		got, err := ParseLearnCombinations(tc.in, tc.active)
		if (err != nil) != tc.wantErr {
			t.Errorf("ParseLearnCombinations(%q, %q) err = %v, wantErr %v", tc.in, tc.active, err, tc.wantErr)
			continue
		}
		if !tc.wantErr && !reflect.DeepEqual(got, tc.want) {
			t.Errorf("ParseLearnCombinations(%q, %q) = %v, want %v", tc.in, tc.active, got, tc.want)
		}
	}
}

func TestVariantManager_LearnSubset(t *testing.T) {
	vm := NewVariantManagerWithOptions(VariantOptions{Combinations: []VariantCombination{"ewma:500", "sliding_window:50"}})

	combos := vm.GetAllCombinations()
	sort.Slice(combos, func(i, j int) bool { return combos[i] < combos[j] })
	if want := []VariantCombination{"ewma:500", "sliding_window:50"}; !reflect.DeepEqual(combos, want) {
		t.Fatalf("GetAllCombinations = %v, want %v", combos, want)
	}
	if n := len(vm.GetAllBaselines()); n != 2 {
		t.Fatalf("GetAllBaselines len = %d, want 2", n)
	}
	if got := vm.GetActiveCombination(); got != "ewma:500" {
		t.Errorf("active = %s, want first learned ewma:500 when cumulative:50 is absent", got)
	}
	if vm.SetActiveCombination("cumulative:50") {
		t.Error("switched to a combination that is not learned")
	}
	if vm.GetCombination("cumulative:50") != nil {
		t.Error("non-learned combination has a baseline")
	}
	if !vm.SetActiveCombination("sliding_window:50") || vm.GetActiveCombination() != "sliding_window:50" {
		t.Error("could not switch to a learned combination")
	}

	vm.UpdateAll(raceTrace("t1", "op"), "fp")
	for _, c := range combos {
		if len(vm.GetCombination(c).GetTopologies("svc")) == 0 {
			t.Errorf("%s did not learn", c)
		}
	}
}

func TestVariantManager_OptionsDefaults(t *testing.T) {
	vm := NewVariantManagerWithOptions(VariantOptions{})
	if n := len(vm.GetAllCombinations()); n != 20 {
		t.Fatalf("empty Combinations learned %d, want 20", n)
	}
	if got := vm.GetActiveCombination(); got != "cumulative:50" {
		t.Errorf("default active = %s, want cumulative:50", got)
	}
	vm = NewVariantManagerWithOptions(VariantOptions{Active: "ewma:500"})
	if got := vm.GetActiveCombination(); got != "ewma:500" {
		t.Errorf("active = %s, want ewma:500", got)
	}
	vm = NewVariantManagerWithOptions(VariantOptions{Combinations: []VariantCombination{"ewma:100", "cumulative:50"}, Active: "ewma:500"})
	if got := vm.GetActiveCombination(); got != "cumulative:50" {
		t.Errorf("active not learned should fall back to cumulative:50, got %s", got)
	}
}
