package main

import (
	"bytes"
	"context"
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
			args:    []string{"teleport"},
			wantErr: errUsage,
		},
		{
			name:    "space needs a subcommand",
			args:    []string{"space"},
			wantErr: errUsage,
		},
		{
			name:    "an unknown space subcommand is a usage error",
			args:    []string{"space", "vacuum"},
			wantErr: errUsage,
		},
		{
			name:    "init needs a store",
			args:    []string{"init"},
			wantErr: errUsage,
		},
		{
			// The flag must be rejected rather than ignored: a user who
			// mistypes it and gets a summary would read that as the full list.
			name:    "an unknown space check flag is a usage error",
			args:    []string{"space", "check", "--everything"},
			wantErr: errUsage,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer

			err := run(context.Background(), tc.args, &out)

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantOutput != "" && !strings.Contains(out.String(), tc.wantOutput) {
				t.Errorf("output = %q, want it to contain %q", out.String(), tc.wantOutput)
			}
		})
	}
}

func TestHumanBytes(t *testing.T) {
	tests := map[int64]string{
		0:                      "0 B",
		512:                    "512 B",
		1024:                   "1.0 KiB",
		1536:                   "1.5 KiB",
		1024 * 1024:            "1.0 MiB",
		3 * 1024 * 1024 * 1024: "3.0 GiB",
	}

	for n, want := range tests {
		if got := humanBytes(n); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}
