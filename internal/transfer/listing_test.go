package transfer

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

func listNames(t *testing.T, base, shareID string) []string {
	t.Helper()
	resp := do(t, "GET", base+"/api/list?share="+shareID+"&dir=", nil, nil)
	defer resp.Body.Close()

	var out listResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	names := make([]string, 0, len(out.Entries))
	for _, e := range out.Entries {
		names = append(names, e.Name)
	}
	sort.Strings(names)
	return names
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// Two independent filters: a dot prefix works on every platform, the hidden
// attribute only exists on Windows.
func TestListingFilters(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"visible.txt", ".dotfile.txt", "secret.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, ".dotdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Folders are filtered by the same rules, so the directory must disappear
	// alongside the dotfile.
	hiddenMarked := false
	if runtime.GOOS == "windows" {
		if err := exec.Command("attrib", "+H", filepath.Join(root, "secret.txt")).Run(); err == nil {
			hiddenMarked = true
		}
	}

	plain := []string{"visible.txt"}
	if !hiddenMarked {
		plain = append(plain, "secret.txt")
	}

	cases := []struct {
		name string
		opt  StartOptions
		want []string
	}{
		{
			name: "default hides both kinds",
			opt:  StartOptions{},
			want: plain,
		},
		{
			name: "dotfiles only",
			opt:  StartOptions{ShowDotfiles: true},
			want: append(append([]string{}, plain...), ".dotfile.txt", ".dotdir"),
		},
		{
			name: "everything",
			opt:  StartOptions{ShowHidden: true, ShowDotfiles: true},
			want: []string{"visible.txt", "secret.txt", ".dotfile.txt", ".dotdir"},
		},
	}

	if hiddenMarked {
		cases = append(cases, struct {
			name string
			opt  StartOptions
			want []string
		}{
			name: "hidden attribute only",
			opt:  StartOptions{ShowHidden: true},
			want: []string{"visible.txt", "secret.txt"},
		})
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, base := newServerAt(t, root, c.opt)
			got := listNames(t, base, "test")
			if !sameSet(got, c.want) {
				t.Fatalf("entries = %v, want %v", got, c.want)
			}
		})
	}
}

// The desktop toggles these while the server is running, so the change has to
// take effect on the next request without a restart.
func TestListingFiltersApplyWhileRunning(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".dotfile.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "visible.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, base := newServerAt(t, root, StartOptions{})

	if got := listNames(t, base, "test"); !sameSet(got, []string{"visible.txt"}) {
		t.Fatalf("before toggle: %v", got)
	}

	s.SetViewOptions(false, true)

	if got := listNames(t, base, "test"); !sameSet(got, []string{"visible.txt", ".dotfile.txt"}) {
		t.Fatalf("after toggle: %v", got)
	}
}
