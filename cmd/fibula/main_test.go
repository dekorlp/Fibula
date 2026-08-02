package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantOutput string
		wantErr    error
	}{
		{
			name:       "no arguments print the usage",
			args:       nil,
			wantOutput: "Usage:",
		},
		{
			name:       "version prints the client version",
			args:       []string{"version"},
			wantOutput: "fibula " + version,
		},
		{
			name:       "help prints the usage",
			args:       []string{"help"},
			wantOutput: "Usage:",
		},
		{
			name:    "an unknown command is a usage error",
			args:    []string{"snapshot"},
			wantErr: errUsage,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer

			err := run(tc.args, &out)

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantOutput != "" && !strings.Contains(out.String(), tc.wantOutput) {
				t.Errorf("output = %q, want it to contain %q", out.String(), tc.wantOutput)
			}
		})
	}
}
