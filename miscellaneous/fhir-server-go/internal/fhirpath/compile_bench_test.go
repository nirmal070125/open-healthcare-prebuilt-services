package fhirpath

import "testing"

// Benchmarks the per-resource extraction cost: evaluate every Patient and
// Observation search expression against a representative resource, the way the
// indexer does on ingest. Compares the generic interpreter vs the compiled
// evaluator (ns/op + allocs/op via -benchmem).

func benchExprsFor(rt string) []string {
	var out []string
	for _, e := range realExprs {
		if len(e) >= len(rt) && e[:len(rt)] == rt {
			out = append(out, e)
		}
	}
	return out
}

func runAll(b *testing.B, exprs []string, res map[string]any, compiled bool) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, e := range exprs {
			if compiled {
				_, _ = EvaluatePolymorphicCompiled(e, res)
			} else {
				_, _ = EvaluatePolymorphic(e, res)
			}
		}
	}
}

func BenchmarkPatientGeneric(b *testing.B) {
	runAll(b, benchExprsFor("Patient"), sampleResources()[0], false)
}
func BenchmarkPatientCompiled(b *testing.B) {
	runAll(b, benchExprsFor("Patient"), sampleResources()[0], true)
}
func BenchmarkObservationGeneric(b *testing.B) {
	runAll(b, benchExprsFor("Observation"), sampleResources()[2], false)
}
func BenchmarkObservationCompiled(b *testing.B) {
	runAll(b, benchExprsFor("Observation"), sampleResources()[2], true)
}
