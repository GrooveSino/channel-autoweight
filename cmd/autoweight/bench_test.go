package main

import (
	"autoweight/firstpass"
	"encoding/json"
	"io"
	"os"
	"testing"
)

func BenchmarkComparison(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if err := reviewAlgorithm("first-pass-v1", "../../testdata/paired", true, io.Discard); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkDecision(b *testing.B) {
	var in firstpass.Input
	raw, err := os.ReadFile("../../testdata/paired/firstpass.json")
	if err != nil {
		b.Fatal(err)
	}
	if err = json.Unmarshal(raw, &in); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := firstpass.Evaluate(in); err != nil {
			b.Fatal(err)
		}
	}
}
