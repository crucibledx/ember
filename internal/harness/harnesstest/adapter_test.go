package harnesstest

import (
	"testing"

	"github.com/crucibledx/ember/internal/harness"
)

func TestValidateSessionRef(t *testing.T) {
	tests := []struct {
		name    string
		ref     harness.SessionRef
		wantErr bool
	}{
		{
			name: "valid nested slash path",
			ref:  harness.SessionRef{RunID: "run-1", Files: []string{"sessions/run-1.jsonl"}},
		},
		{
			name:    "empty run id",
			ref:     harness.SessionRef{Files: []string{"session.jsonl"}},
			wantErr: true,
		},
		{
			name:    "no files",
			ref:     harness.SessionRef{RunID: "run-1"},
			wantErr: true,
		},
		{
			name:    "backslash traversal",
			ref:     harness.SessionRef{RunID: "run-1", Files: []string{`..\outside.jsonl`}},
			wantErr: true,
		},
		{
			name:    "drive qualified",
			ref:     harness.SessionRef{RunID: "run-1", Files: []string{"C:/sessions/run-1.jsonl"}},
			wantErr: true,
		},
		{
			name:    "absolute",
			ref:     harness.SessionRef{RunID: "run-1", Files: []string{"/sessions/run-1.jsonl"}},
			wantErr: true,
		},
		{
			name:    "parent traversal",
			ref:     harness.SessionRef{RunID: "run-1", Files: []string{"../outside.jsonl"}},
			wantErr: true,
		},
		{
			name:    "current directory",
			ref:     harness.SessionRef{RunID: "run-1", Files: []string{"."}},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateSessionRef(test.ref)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateSessionRef() error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
}
