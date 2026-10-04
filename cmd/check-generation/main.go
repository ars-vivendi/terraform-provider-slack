// Command check-generation fails if regeneration changes any generated artifact.
package main

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

func snapshot() map[string][32]byte {
	out := map[string][32]byte{}
	for _, root := range []string{"apis", "internal/controller", "examples-generated", "package/crds", "config"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasPrefix(d.Name(), "zz_") && !strings.HasPrefix(path, "package/crds/") && !strings.HasPrefix(path, "examples-generated/") && path != "config/schema.json" && path != "config/upstream-provenance.json" {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[path] = sha256.Sum256(b)
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			panic(err)
		}
	}
	return out
}

func main() {
	before := snapshot()
	cmd := exec.Command("go", "generate", ".")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		panic(err)
	}
	after := snapshot()
	if !reflect.DeepEqual(before, after) {
		changed := map[string]bool{}
		for path, hash := range before {
			if next, ok := after[path]; !ok || next != hash {
				changed[path] = true
			}
		}
		for path, hash := range after {
			if prev, ok := before[path]; !ok || prev != hash {
				changed[path] = true
			}
		}
		paths := make([]string, 0, len(changed))
		for path := range changed {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		fmt.Fprintln(os.Stderr, "Generation changed artifacts:", strings.Join(paths, ", "))
		os.Exit(1)
	}
	fmt.Printf("Generation deterministic: %d artifacts unchanged\n", len(after))
}
