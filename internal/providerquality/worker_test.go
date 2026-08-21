package providerquality

import (
	"strings"
	"testing"
	"time"
)

func TestMeasureSyntheticStreamHashesButDoesNotPersistOutput(t *testing.T) {
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"MODELDock quality \"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"probe OK\"}}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":4}}\n\n" +
		"data: [DONE]\n\n"
	result := measureSyntheticStream(strings.NewReader(stream), time.Now().Add(-time.Second))
	if !result.Succeeded || result.OutputQualityScore == nil || result.OutputQualityScore.String() != "100.0000" {
		t.Fatalf("unexpected synthetic result: %+v", result)
	}
	if result.ResponseSHA256 == nil || len(*result.ResponseSHA256) != 64 || result.OutputTokens == nil || *result.OutputTokens != 4 {
		t.Fatalf("missing redacted evidence: %+v", result)
	}
}

func TestMeasureSyntheticStreamRejectsWrongOutput(t *testing.T) {
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"not the expected output\"}}]}\n\ndata: [DONE]\n\n"
	result := measureSyntheticStream(strings.NewReader(stream), time.Now().Add(-time.Second))
	if result.OutputQualityScore == nil || result.OutputQualityScore.String() != "0.0000" {
		t.Fatalf("wrong output was accepted: %+v", result)
	}
}
