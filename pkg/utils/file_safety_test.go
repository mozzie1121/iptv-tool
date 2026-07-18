package utils

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeBaseFilename(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{name: "cctv.png"},
		{name: " CCTV.png "},
		{name: "../evil.png", wantErr: true},
		{name: `..\evil.png`, wantErr: true},
		{name: "", wantErr: true},
		{name: ".png", wantErr: false},
		{name: "safe..name.png", wantErr: true},
		{name: "C:evil.png", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SafeBaseFilename(tt.name)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.TrimSpace(tt.name) != got {
				t.Fatalf("got %q, want %q", got, strings.TrimSpace(tt.name))
			}
		})
	}
}

func TestSafeJoinWithinDir(t *testing.T) {
	base := t.TempDir()
	got, err := SafeJoinWithinDir(base, "logo.png")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join(base, "logo.png")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	if _, err := SafeJoinWithinDir(base, "../evil.png"); err == nil {
		t.Fatal("expected traversal path to be rejected")
	}
}
