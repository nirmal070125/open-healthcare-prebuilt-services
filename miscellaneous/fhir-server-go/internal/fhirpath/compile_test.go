package fhirpath

import (
	"reflect"
	"testing"
)

// The compiled evaluator must return EXACTLY what the generic interpreter
// returns for every search-parameter expression. These tests use
// EvaluatePolymorphic (the generic path) as the oracle and assert the compiled
// path agrees, across the real expression shapes and representative resources.

// realExprs is the set of FHIRPath expressions used by FHIR R4 search params
// for the hot resource types (verified against internal/seed/fhir-r4-search-params.csv).
var realExprs = []string{
	// Patient
	"Patient.active",
	"Patient.address",
	"Patient.address.city",
	"Patient.address.use",
	"Patient.birthDate",
	"Patient.deceased.ofType(dateTime)",
	"Patient.deceased.exists",
	"Patient.telecom.where(system='email')",
	"Patient.telecom.where(system='phone')",
	"Patient.name.family",
	"Patient.name.given",
	"Patient.name",
	"Patient.gender",
	"Patient.identifier",
	"Patient.telecom",
	"Patient.generalPractitioner",
	"Patient.managingOrganization",
	"Patient.communication.language",
	// Observation
	"Observation.code",
	"Observation.effective",
	"Observation.category",
	"Observation.value.ofType(CodeableConcept) | Observation.component.value.ofType(CodeableConcept)",
	"Observation.value.ofType(Quantity) | Observation.value.ofType(SampledData) | Observation.component.value.ofType(Quantity) | Observation.component.value.ofType(SampledData)",
	"Observation.component.code",
	"Observation.value.ofType(Quantity) | Observation.value.ofType(SampledData)",
	"Observation.value.ofType(string)",
	"Observation.subject",
	"Observation.subject.where(resolve() is Patient)",
	"Observation.encounter",
	"Observation.status",
	"Observation.performer",
	// fallback shapes (must still match exactly)
	"Patient.extension('http://hl7.org/fhir/us/core/StructureDefinition/us-core-race')",
}

func sampleResources() []map[string]any {
	patient := map[string]any{
		"resourceType":     "Patient",
		"active":           true,
		"gender":           "female",
		"birthDate":        "1980-05-06",
		"deceasedDateTime": "2020-01-02T10:00:00Z",
		"name": []any{
			map[string]any{
				"use":    "official",
				"family": "Smith",
				"given":  []any{"Jane", "Q"},
			},
			map[string]any{
				"family": "Jones",
				"given":  []any{"JJ"},
			},
		},
		"telecom": []any{
			map[string]any{"system": "email", "value": "jane@example.com"},
			map[string]any{"system": "phone", "value": "555-1212"},
			map[string]any{"system": "phone", "value": "555-3434"},
		},
		"identifier": []any{
			map[string]any{
				"system": "http://hospital.example/mrn",
				"value":  "MRN-1",
				"type": map[string]any{
					"coding": []any{
						map[string]any{"system": "http://terminology.hl7.org/CodeSystem/v2-0203", "code": "MR"},
					},
				},
			},
		},
		"address": []any{
			map[string]any{"city": "Boston", "state": "MA", "use": "home", "postalCode": "02118", "country": "US"},
		},
		"communication": []any{
			map[string]any{"language": map[string]any{"coding": []any{map[string]any{"system": "urn:ietf:bcp:47", "code": "en"}}}},
		},
		"generalPractitioner":  []any{map[string]any{"reference": "Practitioner/p1"}},
		"managingOrganization": map[string]any{"reference": "Organization/o1"},
		"extension": []any{
			map[string]any{"url": "http://hl7.org/fhir/us/core/StructureDefinition/us-core-race", "valueString": "x"},
			map[string]any{"url": "other", "valueString": "y"},
		},
	}
	deceasedFalsePatient := map[string]any{
		"resourceType": "Patient",
		"name":         []any{map[string]any{"family": "NoTelecom"}},
	}
	observation := map[string]any{
		"resourceType":      "Observation",
		"status":            "final",
		"effectiveDateTime": "2021-03-04",
		"code": map[string]any{
			"coding": []any{map[string]any{"system": "http://loinc.org", "code": "1234-5", "display": "Test"}},
		},
		"category": []any{
			map[string]any{"coding": []any{map[string]any{"system": "s", "code": "vital-signs"}}},
		},
		"subject":       map[string]any{"reference": "Patient/123"},
		"encounter":     map[string]any{"reference": "Encounter/e1"},
		"performer":     []any{map[string]any{"reference": "Practitioner/p9"}},
		"valueQuantity": map[string]any{"value": 9.5, "unit": "mg", "system": "http://unitsofmeasure.org", "code": "mg"},
		"component": []any{
			map[string]any{
				"code":          map[string]any{"coding": []any{map[string]any{"system": "http://loinc.org", "code": "8480-6"}}},
				"valueQuantity": map[string]any{"value": 120.0, "code": "mmHg"},
			},
			map[string]any{
				"code":                 map[string]any{"coding": []any{map[string]any{"system": "http://loinc.org", "code": "8462-4"}}},
				"valueCodeableConcept": map[string]any{"coding": []any{map[string]any{"system": "s", "code": "cc1"}}},
			},
		},
	}
	observationWithStringVal := map[string]any{
		"resourceType": "Observation",
		"valueString":  "positive",
		"subject":      map[string]any{"reference": "Group/g1"}, // resolve() is Patient -> must NOT match
	}
	return []map[string]any{patient, deceasedFalsePatient, observation, observationWithStringVal}
}

func TestCompiledMatchesGeneric(t *testing.T) {
	resources := sampleResources()
	for _, expr := range realExprs {
		for ri, res := range resources {
			want, errWant := EvaluatePolymorphic(expr, res)
			got, errGot := EvaluatePolymorphicCompiled(expr, res)
			if (errWant == nil) != (errGot == nil) {
				t.Errorf("expr %q res#%d: err mismatch: generic=%v compiled=%v", expr, ri, errWant, errGot)
				continue
			}
			if !reflect.DeepEqual(want, got) {
				t.Errorf("expr %q res#%d:\n generic = %#v\ncompiled = %#v", expr, ri, want, got)
			}
		}
	}
}

// TestCompiledFastPathClassification documents which shapes take the fast path
// vs fall back, guarding against accidental regressions in coverage.
func TestCompiledFastPathClassification(t *testing.T) {
	cases := map[string]bool{ // expr -> expected fastPath
		"Patient.name.family":                             true,
		"Patient.telecom.where(system='email')":           true,
		"Patient.deceased.exists":                         true,
		"Observation.subject.where(resolve() is Patient)": true,
		"Observation.value.ofType(Quantity)":              true, // becomes valueQuantity (plain path)
		"Patient.extension('http://x')":                   false,
	}
	for expr, wantFast := range cases {
		if got := compile(expr).fastPath; got != wantFast {
			t.Errorf("expr %q: fastPath=%v, want %v", expr, got, wantFast)
		}
	}
}
