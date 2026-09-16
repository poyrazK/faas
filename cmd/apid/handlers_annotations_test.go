package main

import (
	"strings"
	"testing"
)

func TestValidateAnnotationFormCountsReasonCharacters(t *testing.T) {
	for _, reason := range []string{
		strings.Repeat("a", 280),
		strings.Repeat("é", 280),
		strings.Repeat("界", 280),
		strings.Repeat("🚀", 280),
		strings.Repeat("aé界🚀", 70),
	} {
		if problem := validateAnnotationForm(annotationForm{Reason: reason}); problem != nil {
			t.Fatalf("280-character reason rejected: %#v", problem)
		}
	}
	for _, reason := range []string{strings.Repeat("a", 281), strings.Repeat("🚀", 281)} {
		if problem := validateAnnotationForm(annotationForm{Reason: reason}); problem == nil {
			t.Fatal("281-character reason was accepted")
		}
	}
}
