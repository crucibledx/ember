package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func sizedFile(t *testing.T, dir, name string, size int64) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	at := sizedFile(t, dir, "at", 80*mib)
	over := sizedFile(t, dir, "over", 80*mib+1)
	big := sizedFile(t, dir, "big", 81*mib)
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{
			name: "at the limit passes", args: []string{"80", at}, wantCode: 0,
			wantStdout: "ok   at 80.0 MiB (83886080 bytes)\n",
		},
		{
			name: "one byte over fails", args: []string{"80", at, over}, wantCode: 1,
			wantStdout: "ok   at 80.0 MiB (83886080 bytes)\nFAIL over 80.0 MiB (83886081 bytes) exceeds 80 MiB\n",
		},
		{
			name: "81 MiB fails", args: []string{"80", big}, wantCode: 1,
			wantStdout: "FAIL big 81.0 MiB (84934656 bytes) exceeds 80 MiB\n",
		},
		{
			name: "missing file fails", args: []string{"80", filepath.Join(dir, "gone")}, wantCode: 1,
			wantStdout: "FAIL " + filepath.Join(dir, "gone") + " is missing\n",
		},
		{
			name: "directory counts as missing", args: []string{"80", dir}, wantCode: 1,
			wantStdout: "FAIL " + dir + " is missing\n",
		},
		{
			name: "limit must be a whole number", args: []string{"8O", at}, wantCode: 2,
			wantStderr: "MAX_MIB must be a whole number, got \"8O\"\n",
		},
		{
			name: "negative limit", args: []string{"-1", at}, wantCode: 2,
			wantStderr: "MAX_MIB must be a whole number, got \"-1\"\n",
		},
		{
			name: "no files", args: []string{"80"}, wantCode: 2,
			wantStderr: "usage: binarysize MAX_MIB FILE [FILE ...]\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tt.args, &stdout, &stderr); code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
			if stdout.String() != tt.wantStdout || stderr.String() != tt.wantStderr {
				t.Errorf("stdout %q, stderr %q\nwant stdout %q, stderr %q", stdout.String(), stderr.String(), tt.wantStdout, tt.wantStderr)
			}
		})
	}
}
