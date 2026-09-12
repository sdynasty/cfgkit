package main

import "testing"

func TestIsIdent(t *testing.T) {
	valid := []string{"id", "stack_max", "a1", "_x", "AI_Params"}
	for _, s := range valid {
		if !isIdent(s) {
			t.Errorf("isIdent(%q) = false, 期望 true", s)
		}
	}
	invalid := []string{"", "1a", "a-b", "a b", "a.b", "名字#", "a@b"}
	for _, s := range invalid {
		if isIdent(s) {
			t.Errorf("isIdent(%q) = true, 期望 false", s)
		}
	}
}

func TestUpperCamel(t *testing.T) {
	tests := map[string]string{
		"id":          "Id",
		"stack_max":   "StackMax",
		"hp":          "Hp",
		"ai_params":   "AiParams",
		"_leading":    "Leading",
		"a__b":        "AB",
		"already":     "Already",
		"with_digit1": "WithDigit1",
	}
	for in, want := range tests {
		if got := upperCamel(in); got != want {
			t.Errorf("upperCamel(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

func TestLowerFirst(t *testing.T) {
	tests := map[string]string{
		"ItemQuality": "itemQuality",
		"X":           "x",
		"x":           "x",
		"":            "",
	}
	for in, want := range tests {
		if got := lowerFirst(in); got != want {
			t.Errorf("lowerFirst(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

func TestToSnake(t *testing.T) {
	tests := map[string]string{
		"Item":        "item",
		"MonsterDrop": "monster_drop",
		"item":        "item",
	}
	for in, want := range tests {
		if got := toSnake(in); got != want {
			t.Errorf("toSnake(%q) = %q, 期望 %q", in, got, want)
		}
	}
}
