package adapters

import (
	"strings"
	"testing"
)

func TestWhisperXReadinessImportStatementCoversRuntimeDependencies(t *testing.T) {
	statement := whisperXReadinessImportStatement()

	for _, expected := range []string{
		"import whisperx",
		`importlib.import_module("scipy.special._gufuncs")`,
		"from transformers import Pipeline",
	} {
		if !strings.Contains(statement, expected) {
			t.Fatalf("readiness probe missing %q in %q", expected, statement)
		}
	}
}
