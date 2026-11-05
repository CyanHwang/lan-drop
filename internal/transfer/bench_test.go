package transfer

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkLANIPs(b *testing.B) {
	for i := 0; i < b.N; i++ {
		LANIPs()
	}
}

// Info backs /api/info and every desktop status refresh.
func BenchmarkInfo(b *testing.B) {
	root := b.TempDir()
	s := New(nil)
	if _, err := s.StartWithOptions([]Share{{ID: "t", Name: "t", Path: root}}, StartOptions{Port: 0}); err != nil {
		b.Fatal(err)
	}
	defer s.Stop()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.Info()
	}
}

// A folder with a few thousand entries is the worst realistic listing case:
// every entry costs a stat, a filter check and a JSON object.
func BenchmarkListLargeDir(b *testing.B) {
	const files = 3000

	root := b.TempDir()
	for i := 0; i < files; i++ {
		name := filepath.Join(root, fmt.Sprintf("file-%04d.dat", i))
		if err := os.WriteFile(name, nil, 0o644); err != nil {
			b.Fatal(err)
		}
	}

	_, base := newServerAt(b, root, StartOptions{})
	url := base + "/api/list?share=test&dir="

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := http.Get(url)
		if err != nil {
			b.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// With filter rules configured every entry is matched against each rule.
func BenchmarkListLargeDirWithFilter(b *testing.B) {
	const files = 3000

	root := b.TempDir()
	for i := 0; i < files; i++ {
		name := filepath.Join(root, fmt.Sprintf("file-%04d.dat", i))
		if err := os.WriteFile(name, nil, 0o644); err != nil {
			b.Fatal(err)
		}
	}

	_, base := newServerAt(b, root, StartOptions{
		Filter: `*.tmp, node_modules, .git, re:^\.DS_Store$, re:^build$`,
	})
	url := base + "/api/list?share=test&dir="

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := http.Get(url)
		if err != nil {
			b.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}
