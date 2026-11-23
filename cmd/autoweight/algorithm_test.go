package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestComparison(t *testing.T) {
	var b bytes.Buffer
	if err := reviewAlgorithm("first-pass-v1", "../../testdata/paired", true, &b); err != nil {
		t.Fatal(err)
	}
	var d struct {
		Delta map[int]float64 `json:"target_delta"`
	}
	if err := json.Unmarshal(b.Bytes(), &d); err != nil || len(d.Delta) != 2 || d.Delta[101] != 0 || d.Delta[102] != -1 {
		t.Fatal(b.String(), err)
	}
	if reviewAlgorithm("typo", "../../testdata/paired", false, &b) == nil {
		t.Fatal("unknown policy accepted")
	}
}
