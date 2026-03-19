package webdav

import "testing"

func TestPathMatchesMountBoundary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		request    string
		mount      string
		shouldFind bool
	}{
		{name: "exact match", request: "/minio", mount: "/minio", shouldFind: true},
		{name: "sub path match", request: "/minio/a.txt", mount: "/minio", shouldFind: true},
		{name: "prefix without boundary", request: "/minio/a.txt", mount: "/min", shouldFind: false},
		{name: "other path", request: "/backup/a.txt", mount: "/minio", shouldFind: false},
		{name: "root mount", request: "/anything", mount: "/", shouldFind: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := pathMatchesMount(tt.request, tt.mount)
			if got != tt.shouldFind {
				t.Fatalf("pathMatchesMount(%q, %q)=%v, want %v", tt.request, tt.mount, got, tt.shouldFind)
			}
		})
	}
}

func TestNormalizeMountPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "minio", want: "/minio"},
		{in: "/minio/", want: "/minio"},
		{in: " /a/b ", want: "/a/b"},
		{in: "", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := normalizeMountPath(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("normalizeMountPath(%q) expected error", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeMountPath(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("normalizeMountPath(%q)=%q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
